package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silirim/vodmax/internal/library"
	"github.com/silirim/vodmax/internal/provider"
	"github.com/silirim/vodmax/internal/store"
)

// Images (channel logos for now) are hosted by providers or third parties.
// Clients must not fetch them directly, so the server fetches them through
// the subscription's VPN, validates and caches them. Image URLs given to
// clients are signed so they need no token (an <img> cannot send one) and
// cannot be used to make the server fetch anything else.

const (
	maxImageBytes   = 1 << 20
	imageRetryAfter = 24 * time.Hour // failed fetches are retried after this
	// A fetch that could not reach the source (VPN down, timeout) is not
	// an image problem: it is retried sooner, but not at every request.
	imageRetryUnreachable = 10 * time.Minute
)

var allowedImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// ErrNoImage means the item has no usable image.
var ErrNoImage = errors.New("immagine non disponibile")

// ErrImageUnreachable means the image's source could not be reached a
// moment ago; it is tried again after imageRetryUnreachable.
var ErrImageUnreachable = errors.New("immagine non raggiungibile per ora")

type imageState struct {
	once     sync.Once
	key      []byte
	mu       sync.Mutex
	inflight map[string]*imageCall
	slots    chan struct{}
	failed   map[string]time.Time // unreachable sources, by URL hash
}

type imageCall struct {
	done chan struct{}
	img  store.CachedImage
	err  error
}

func (s *Service) imageKey() []byte {
	s.images.once.Do(func() {
		s.images.key = s.box.Derive("image-url-signing")
		s.images.inflight = map[string]*imageCall{}
		s.images.slots = make(chan struct{}, 4)
		s.images.failed = map[string]time.Time{}
	})
	return s.images.key
}

func (s *Service) imageSignature(itemID int64) string {
	return s.signImage("logo:" + strconv.FormatInt(itemID, 10))
}

// LogoURL is the signed local URL of an item's image.
func (s *Service) LogoURL(itemID int64) string {
	return "/img/logo/" + strconv.FormatInt(itemID, 10) + "?s=" + s.imageSignature(itemID)
}

// EpisodeThumbURL is the signed local URL of an episode's picture.
func (s *Service) EpisodeThumbURL(episodeID int64) string {
	return "/img/episode/" + strconv.FormatInt(episodeID, 10) + "?s=" + s.signImage("episode:"+strconv.FormatInt(episodeID, 10))
}

func (s *Service) signImage(what string) string {
	mac := hmac.New(sha256.New, s.imageKey())
	mac.Write([]byte(what))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// EpisodeThumb returns the picture of an episode, like Logo.
func (s *Service) EpisodeThumb(ctx context.Context, episodeID int64, sig string) (store.CachedImage, error) {
	if !hmac.Equal([]byte(sig), []byte(s.signImage("episode:"+strconv.FormatInt(episodeID, 10)))) {
		return store.CachedImage{}, ErrBadSignature
	}
	src, subID, err := s.st.EpisodeImage(ctx, episodeID)
	if err != nil {
		return store.CachedImage{}, err
	}
	if strings.HasPrefix(src, library.ImageScheme) {
		return s.localImage(ctx, subID, src)
	}
	if !imageURL(src) {
		return store.CachedImage{}, ErrNoImage
	}
	return s.cachedImage(ctx, src, func(ctx context.Context) (store.CachedImage, error) { return s.fetchImage(ctx, subID, src) })
}

// ErrBadSignature is returned for image URLs not issued by this server.
var ErrBadSignature = errors.New("firma dell'immagine non valida")

// Logo returns the image of an item, from the cache or fetched through the
// subscription's VPN.
func (s *Service) Logo(ctx context.Context, itemID int64, sig string) (store.CachedImage, error) {
	if !hmac.Equal([]byte(sig), []byte(s.imageSignature(itemID))) {
		return store.CachedImage{}, ErrBadSignature
	}
	src, subID, err := s.st.ItemImage(ctx, itemID)
	if err != nil {
		return store.CachedImage{}, err
	}
	if strings.HasPrefix(src, library.ImageScheme) {
		if img, err := s.localImage(ctx, subID, src); err == nil {
			return img, nil
		}
		src = "" // no picture in the folder: TMDB's, if known
	}
	if !imageURL(src) {
		if poster := s.tmdbPoster(ctx, itemID); poster != "" {
			return s.cachedImage(ctx, poster, func(ctx context.Context) (store.CachedImage, error) { return s.fetchDirect(ctx, poster) })
		}
		return store.CachedImage{}, ErrNoImage
	}
	img, err := s.cachedImage(ctx, src, func(ctx context.Context) (store.CachedImage, error) { return s.fetchImage(ctx, subID, src) })
	if errors.Is(err, ErrNoImage) {
		// Broken or refused by the provider: the TMDB poster, if known.
		if poster := s.tmdbPoster(ctx, itemID); poster != "" {
			return s.cachedImage(ctx, poster, func(ctx context.Context) (store.CachedImage, error) { return s.fetchDirect(ctx, poster) })
		}
	}
	return img, err
}

// cachedImage returns src from the cache or fetches it once, even if many
// clients ask at the same time. Failures the source reports (ErrNoImage)
// are cached too, for imageRetryAfter.
func (s *Service) cachedImage(ctx context.Context, src string, fetch func(context.Context) (store.CachedImage, error)) (store.CachedImage, error) {
	sum := sha256.Sum256([]byte(src))
	hash := hex.EncodeToString(sum[:])
	if img, ok, err := s.st.CachedImage(ctx, hash); err != nil {
		return img, err
	} else if ok && (img.ContentType != "" || time.Since(img.FetchedAt) < imageRetryAfter) {
		if img.ContentType == "" {
			return img, ErrNoImage
		}
		return img, nil
	}
	s.imageKey()
	s.images.mu.Lock()
	if at, ok := s.images.failed[hash]; ok && time.Since(at) < imageRetryUnreachable {
		s.images.mu.Unlock()
		return store.CachedImage{}, ErrImageUnreachable
	}
	call := s.images.inflight[hash]
	if call == nil {
		// Another request may have just stored it.
		if img, ok, err := s.st.CachedImage(ctx, hash); err == nil && ok && img.ContentType != "" {
			s.images.mu.Unlock()
			return img, nil
		}
		call = &imageCall{done: make(chan struct{})}
		s.images.inflight[hash] = call
		s.images.mu.Unlock()
		go func() {
			call.img, call.err = fetch(context.WithoutCancel(ctx))
			if call.err == nil || errors.Is(call.err, ErrNoImage) {
				s.st.PutCachedImage(context.Background(), hash, call.img)
			}
			s.images.mu.Lock()
			delete(s.images.inflight, hash)
			if call.err != nil && !errors.Is(call.err, ErrNoImage) {
				// Logged once here, at info: a channel list with the VPN
				// down would otherwise log an error per logo per page.
				slog.Info("immagine non scaricata: nuovo tentativo tra qualche minuto", "err", call.err)
				now := time.Now()
				for h, at := range s.images.failed {
					if now.Sub(at) >= imageRetryUnreachable {
						delete(s.images.failed, h)
					}
				}
				s.images.failed[hash] = now
				call.err = ErrImageUnreachable
			}
			s.images.mu.Unlock()
			close(call.done)
		}()
	} else {
		s.images.mu.Unlock()
	}
	select {
	case <-call.done:
	case <-ctx.Done():
		return store.CachedImage{}, ctx.Err()
	}
	if call.err != nil {
		return store.CachedImage{}, call.err
	}
	if call.img.ContentType == "" {
		return call.img, ErrNoImage
	}
	return call.img, nil
}

// fetchImage downloads and validates an image through the VPN. Images the
// source refuses or that are not acceptable images return an empty
// CachedImage with ErrNoImage (cached as a failure); transport errors
// (VPN down) are not cached.
func (s *Service) fetchImage(ctx context.Context, subID int64, src string) (store.CachedImage, error) {
	s.images.slots <- struct{}{}
	defer func() { <-s.images.slots }()
	sub, err := s.st.Subscription(ctx, subID)
	if err != nil {
		return store.CachedImage{}, err
	}
	tun, err := s.tunnel(ctx, sub.VPNID)
	if err != nil {
		return store.CachedImage{}, fmt.Errorf("VPN: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return store.CachedImage{}, ErrNoImage
	}
	req.Header.Set("User-Agent", "VodMax")
	if _, ok := tun.(systemNetwork); ok {
		tun = imageNetwork{}
	}
	client := provider.NewHTTPClient(tun, 0)
	defer client.CloseIdleConnections() // single use: no idle connection left for 90 s
	resp, err := client.Do(req)
	if errors.Is(err, errLocalAddress) {
		return store.CachedImage{}, ErrNoImage // remembered like a broken image
	}
	if err != nil {
		return store.CachedImage{}, fmt.Errorf("immagine: %w", provider.Redact(err))
	}
	return readImage(resp)
}

// fetchDirect downloads an image that is not the provider's (TMDB): the
// normal network, same checks.
func (s *Service) fetchDirect(ctx context.Context, src string) (store.CachedImage, error) {
	s.images.slots <- struct{}{}
	defer func() { <-s.images.slots }()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return store.CachedImage{}, ErrNoImage
	}
	req.Header.Set("User-Agent", "VodMax")
	resp, err := s.directHTTP().Do(req)
	if err != nil {
		return store.CachedImage{}, fmt.Errorf("immagine: %w", provider.Redact(err))
	}
	return readImage(resp)
}

func imageURL(src string) bool {
	u, err := url.Parse(src)
	return src != "" && err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func readImage(resp *http.Response) (store.CachedImage, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return store.CachedImage{}, ErrNoImage
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return store.CachedImage{}, fmt.Errorf("immagine: %w", provider.Redact(err))
	}
	ctype := http.DetectContentType(data)
	if len(data) > maxImageBytes || !allowedImageTypes[ctype] {
		return store.CachedImage{}, ErrNoImage
	}
	return store.CachedImage{ContentType: ctype, Data: data, FetchedAt: time.Now()}, nil
}

// withLogos sets the signed logo URL of channels that have an image.
func (s *Service) withLogos(list []store.TVChannel) {
	for i := range list {
		if list[i].Image != "" {
			list[i].Logo = s.LogoURL(list[i].ID)
		}
		list[i].Image = "" // never hand provider URLs to clients
	}
}
