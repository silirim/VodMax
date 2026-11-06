package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silirim/vodmax/internal/catalog"
	"github.com/silirim/vodmax/internal/library"
	"github.com/silirim/vodmax/internal/playlist/xtream"
	"github.com/silirim/vodmax/internal/store"
	"github.com/silirim/vodmax/internal/tmdb"
)

// Film and series details come from the provider (through the VPN, when
// a title is opened) and, with the user's TMDB key, from TMDB: Italian
// overview, poster when the provider's is missing or broken, backdrop,
// cast. Both are cached; TMDB is not the provider and is reached directly.

const (
	metaFresh      = 30 * 24 * time.Hour
	metaRetry      = 24 * time.Hour // after an empty or failed answer
	tmdbKeySetting = "secret:tmdb_key"
	secretPrefix   = "secret:"
)

type metaState struct {
	mu       sync.Mutex
	http     *http.Client
	tmdbBase string // tests
}

func (s *Service) directHTTP() *http.Client {
	s.meta.mu.Lock()
	defer s.meta.mu.Unlock()
	if s.meta.http == nil {
		s.meta.http = &http.Client{Timeout: 20 * time.Second}
	}
	return s.meta.http
}

// SetTMDBBase points TMDB elsewhere (tests).
func (s *Service) SetTMDBBase(base string) {
	s.meta.mu.Lock()
	s.meta.tmdbBase = base
	s.meta.mu.Unlock()
}

func (s *Service) newTMDB(key string) *tmdb.Client {
	c := tmdb.New(key, s.directHTTP())
	s.meta.mu.Lock()
	base := s.meta.tmdbBase
	s.meta.mu.Unlock()
	if base != "" {
		c.WithBase(base)
	}
	return c
}

// secretSetting stores a sealed value in the settings table.
func (s *Service) secretSetting(ctx context.Context, key string) (string, error) {
	var b64 string
	if err := s.st.Setting(ctx, key, &b64); err != nil || b64 == "" {
		return "", err
	}
	plain, err := s.openSealedValue(key, b64)
	return string(plain), err
}

func (s *Service) openSealedValue(key, b64 string) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	return s.box.Open(sealed, "setting:"+key)
}

func (s *Service) setSecretSetting(ctx context.Context, key, value string) error {
	if value == "" {
		return s.st.DeleteSetting(ctx, key)
	}
	return s.st.SetSetting(ctx, key, base64.StdEncoding.EncodeToString(s.box.Seal([]byte(value), "setting:"+key)))
}

func (s *Service) tmdbClient(ctx context.Context) *tmdb.Client {
	key, err := s.secretSetting(ctx, tmdbKeySetting)
	if err != nil || key == "" {
		return nil
	}
	return s.newTMDB(key)
}

// MetadataSettings says whether TMDB is configured (the key is never
// returned).
type MetadataSettings struct {
	TMDB bool `json:"tmdb"`
}

func (s *Service) MetadataSettings(ctx context.Context) MetadataSettings {
	return MetadataSettings{TMDB: s.tmdbClient(ctx) != nil}
}

// SetTMDBKey checks the key with TMDB and stores it; "" removes it.
func (s *Service) SetTMDBKey(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key != "" {
		if err := s.newTMDB(key).Check(ctx); err != nil {
			if errors.Is(err, tmdb.ErrKey) {
				return invalid("TMDB rifiuta questa chiave: copia la “Chiave API” o il “Token di accesso in lettura” dalle impostazioni API del tuo account TMDB")
			}
			return invalid("TMDB non raggiungibile: %v", err)
		}
	}
	return s.setSecretSetting(ctx, tmdbKeySetting, key)
}

// ItemDetails is the detail page of a film or series.
type ItemDetails struct {
	Title         string   `json:"title,omitempty"` // Italian title from TMDB
	OriginalTitle string   `json:"original_title,omitempty"`
	Plot          string   `json:"plot,omitempty"`
	Cast          string   `json:"cast,omitempty"`
	Director      string   `json:"director,omitempty"`
	Genre         string   `json:"genre,omitempty"`
	Released      string   `json:"released,omitempty"`
	Country       string   `json:"country,omitempty"`
	Rating        float64  `json:"rating,omitempty"`
	Duration      int      `json:"duration,omitempty"` // seconds
	Trailer       string   `json:"trailer,omitempty"`  // YouTube id
	Backdrop      string   `json:"backdrop,omitempty"` // signed local URL
	Sources       []string `json:"sources"`
	Local         bool     `json:"local,omitempty"` // a file of a collection (nothing to download)
}

func (s *Service) Details(ctx context.Context, itemID int64) (ItemDetails, error) {
	if err := s.allowItem(ctx, itemID); err != nil {
		return ItemDetails{}, err
	}
	m, err := s.st.MetaItem(ctx, itemID)
	if err != nil {
		return ItemDetails{}, err
	}
	if m.Kind != "movie" && m.Kind != "series" {
		return ItemDetails{}, invalid("schede solo per film e serie")
	}
	d, _ := s.itemDetails(ctx, m) // a page with what is known
	if d.Cast != "" || d.Director != "" {
		s.st.PutItemPeople(context.WithoutCancel(ctx), m.ID, peopleText(d.Cast, d.Director))
	}
	return d, nil
}

// itemDetails builds the detail page of a film or series, from the
// provider (or the collection's files) and TMDB; the error says why the
// provider's page is missing, the details hold what is known anyway.
func (s *Service) itemDetails(ctx context.Context, m store.MetaItem) (ItemDetails, error) {
	p, err := s.providerDetailsChecked(ctx, m)
	t := s.tmdbTitle(ctx, m, p.TMDB)
	d := ItemDetails{Sources: []string{}, Title: t.Title, OriginalTitle: first(p.OriginalTitle, t.OriginalTitle),
		Plot: first(p.Plot, t.Overview), Cast: first(p.Cast, strings.Join(t.Cast, ", ")), Director: first(p.Director, t.Director),
		Genre: first(p.Genre, strings.Join(t.Genres, ", ")), Released: first(p.Released, t.Released), Country: p.Country,
		Rating: p.Rating, Duration: p.DurationSecs, Trailer: first(p.Trailer, t.Trailer)}
	if d.Rating == 0 {
		d.Rating = t.Rating
	}
	if d.Duration == 0 {
		d.Duration = t.Runtime * 60
	}
	if (len(p.Backdrops) > 0 && (imageURL(p.Backdrops[0]) || strings.HasPrefix(p.Backdrops[0], library.ImageScheme))) || t.Backdrop != "" {
		d.Backdrop = "/img/meta/" + strconv.FormatInt(m.ID, 10) + "/backdrop?s=" + s.metaSignature(m.ID, "backdrop")
	}
	if p.Plot != "" || p.Cast != "" || p.Genre != "" || len(p.Backdrops) > 0 {
		d.Sources = append(d.Sources, "provider")
	}
	if t.Title != "" {
		d.Sources = append(d.Sources, "tmdb")
	}
	_, d.Local, _ = s.local(ctx, m.SubscriptionID)
	return d, err
}

func first(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// providerDetails: cached for metaFresh (metaRetry when empty); errors
// leave the page with what is known.
func (s *Service) providerDetails(ctx context.Context, m store.MetaItem) xtream.Details {
	d, _ := s.providerDetailsChecked(ctx, m)
	return d
}

// providerDetailsChecked also says why the page is not known: the
// provider was asked and did not answer, or answered something unreadable
// (xtream.ErrUnreadable).
func (s *Service) providerDetailsChecked(ctx context.Context, m store.MetaItem) (xtream.Details, error) {
	key := "p:" + strconv.FormatInt(m.ID, 10)
	var d xtream.Details
	if data, at, ok, _ := s.st.Meta(ctx, key); ok {
		json.Unmarshal([]byte(data), &d)
		if age := time.Since(at); age < metaFresh && (data != "{}" || age < metaRetry) {
			return d, nil
		}
	}
	sub, err := s.st.Subscription(ctx, m.SubscriptionID)
	if err == nil && sub.Kind == "local" {
		return s.localDetails(ctx, m), nil // read from the folder each time: cheap, always current
	}
	if err != nil || sub.Kind != "xtream" {
		return d, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client, err := s.xtreamClient(ctx, m.SubscriptionID, 12*time.Second)
	if err != nil {
		return d, err
	}
	var fresh xtream.Details
	if m.Kind == "movie" {
		fresh, err = client.VODInfo(ctx, m.SourceID)
	} else {
		var info xtream.SeriesInfo
		info, err = client.SeriesInfo(ctx, m.SourceID)
		fresh = info.Details
	}
	if err != nil {
		return d, err // try again next time
	}
	raw, _ := json.Marshal(fresh)
	s.st.PutMeta(context.WithoutCancel(ctx), key, string(raw))
	return fresh, nil
}

func (s *Service) tmdbTitle(ctx context.Context, m store.MetaItem, fallbackID string) tmdb.Title {
	id := first(m.TMDBID, fallbackID)
	if _, err := strconv.ParseUint(id, 10, 64); err != nil || id == "0" {
		return tmdb.Title{}
	}
	kind := map[string]string{"movie": "movie", "series": "tv"}[m.Kind]
	key := "t:" + kind + ":" + id
	var t tmdb.Title
	if data, at, ok, _ := s.st.Meta(ctx, key); ok {
		json.Unmarshal([]byte(data), &t)
		if age := time.Since(at); age < metaFresh && (data != "{}" || age < metaRetry) {
			return t
		}
	}
	c := s.tmdbClient(ctx)
	if c == nil {
		return t
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var fresh tmdb.Title
	var err error
	if kind == "movie" {
		fresh, err = c.Movie(ctx, id)
	} else {
		fresh, err = c.TV(ctx, id)
	}
	if err != nil && !strings.Contains(err.Error(), "non trovato") {
		return t // network or key problem: not cached
	}
	raw, _ := json.Marshal(fresh)
	s.st.PutMeta(context.WithoutCancel(ctx), key, string(raw))
	return fresh
}

// tmdbPoster is the TMDB poster of a film or series, "" if unknown.
func (s *Service) tmdbPoster(ctx context.Context, itemID int64) string {
	m, err := s.st.MetaItem(ctx, itemID)
	if err != nil || (m.Kind != "movie" && m.Kind != "series") || m.TMDBID == "" {
		return ""
	}
	return s.tmdbTitle(ctx, m, "").Poster
}

func (s *Service) metaSignature(itemID int64, which string) string {
	mac := hmac.New(sha256.New, s.imageKey())
	mac.Write([]byte(which + ":" + strconv.FormatInt(itemID, 10)))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// MetaImage serves the backdrop of a detail page.
func (s *Service) MetaImage(ctx context.Context, itemID int64, which, sig string) (store.CachedImage, error) {
	if which != "backdrop" || !hmac.Equal([]byte(sig), []byte(s.metaSignature(itemID, which))) {
		return store.CachedImage{}, ErrBadSignature
	}
	m, err := s.st.MetaItem(ctx, itemID)
	if err != nil {
		return store.CachedImage{}, err
	}
	p := s.providerDetails(ctx, m)
	if len(p.Backdrops) > 0 && strings.HasPrefix(p.Backdrops[0], library.ImageScheme) {
		if img, err := s.localImage(ctx, m.SubscriptionID, p.Backdrops[0]); err == nil {
			return img, nil
		}
	}
	if len(p.Backdrops) > 0 && imageURL(p.Backdrops[0]) {
		src := p.Backdrops[0]
		img, err := s.cachedImage(ctx, src, func(ctx context.Context) (store.CachedImage, error) { return s.fetchImage(ctx, m.SubscriptionID, src) })
		if !errors.Is(err, ErrNoImage) {
			return img, err
		}
	}
	if t := s.tmdbTitle(ctx, m, p.TMDB); t.Backdrop != "" {
		return s.cachedImage(ctx, t.Backdrop, func(ctx context.Context) (store.CachedImage, error) { return s.fetchDirect(ctx, t.Backdrop) })
	}
	return store.CachedImage{}, ErrNoImage
}

// ---- Trending: TMDB's week, as far as the catalog has it ----

const (
	trendingFor   = 6 * time.Hour
	trendingRetry = 15 * time.Minute // after a failure
)

type trendingCache struct {
	mu       sync.Mutex
	at       time.Time
	list     map[string][]tmdb.Trend // movie, tv
	fetching bool
}

// Trending lists the titles of the catalog in TMDB's weekly ranking,
// films and series alternating, at most 24. Without a TMDB key: none.
func (s *Service) Trending(ctx context.Context) ([]store.CatalogItem, error) {
	c := s.tmdbClient(ctx)
	if c == nil {
		return []store.CatalogItem{}, nil
	}
	// One request fetches, outside the lock: the others use the list they
	// have (none the first time) instead of queueing behind TMDB for up
	// to 30 s. A failure keeps the old list and tries again later, not at
	// every home page.
	s.trend.mu.Lock()
	if time.Since(s.trend.at) > trendingFor && !s.trend.fetching {
		s.trend.fetching = true
		s.trend.mu.Unlock()
		list := map[string][]tmdb.Trend{}
		var err error
		for _, kind := range []string{"movie", "tv"} {
			// A closed page must not count as a TMDB failure.
			if list[kind], err = c.Trending(context.WithoutCancel(ctx), kind); err != nil && len(list[kind]) == 0 {
				break
			}
		}
		s.trend.mu.Lock()
		s.trend.fetching = false
		if err == nil {
			s.trend.list, s.trend.at = list, time.Now()
		} else {
			if s.trend.list == nil {
				s.trend.list = list // what came, until the next try
			}
			s.trend.at = time.Now().Add(trendingRetry - trendingFor)
		}
	}
	ranking := s.trend.list
	s.trend.mu.Unlock()

	matched := map[string][]store.CatalogItem{}
	for kind, catalogKind := range map[string]string{"movie": "movie", "tv": "series"} {
		var ids, keys []string
		for _, t := range ranking[kind] {
			ids = append(ids, t.ID)
			keys = append(keys, catalog.GroupKey(catalogKind, t.Title, t.Year), catalog.GroupKey(catalogKind, t.Title, 0))
			if t.OriginalTitle != "" && t.OriginalTitle != t.Title {
				keys = append(keys, catalog.GroupKey(catalogKind, t.OriginalTitle, t.Year), catalog.GroupKey(catalogKind, t.OriginalTitle, 0))
			}
		}
		byID, byKey, err := s.st.TitlesByTMDB(ctx, catalogKind, ids, keys, viewerOf(ctx).Kids)
		if err != nil {
			return nil, err
		}
		seen := map[int64]bool{}
		for _, t := range ranking[kind] {
			it, ok := byID[t.ID]
			for _, k := range []string{catalog.GroupKey(catalogKind, t.Title, t.Year), catalog.GroupKey(catalogKind, t.Title, 0),
				catalog.GroupKey(catalogKind, t.OriginalTitle, t.Year), catalog.GroupKey(catalogKind, t.OriginalTitle, 0)} {
				if !ok {
					it, ok = byKey[k]
				}
			}
			if ok && !seen[it.ID] {
				seen[it.ID] = true
				matched[kind] = append(matched[kind], it)
			}
		}
	}
	out := []store.CatalogItem{}
	for i := 0; len(out) < 24 && (i < len(matched["movie"]) || i < len(matched["tv"])); i++ {
		if i < len(matched["movie"]) {
			out = append(out, matched["movie"][i])
		}
		if i < len(matched["tv"]) && len(out) < 24 {
			out = append(out, matched["tv"][i])
		}
	}
	s.catalogImages(ctx, out)
	return out, nil
}
