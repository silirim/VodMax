package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/silirim/vodmax/internal/library"
	"github.com/silirim/vodmax/internal/store"
)

// Copies on a device (decision 0056): a film, an episode or a recording
// already on the server's disk goes to a phone or a USB drive through a
// signed link under /stream, which a login in front of VodMax leaves open.
// The link is kept nowhere: its signature covers the file (size and
// modification time), the device with its pairing date (SQLite reuses
// ids) and the profile with its PIN epoch, so removing the device,
// changing the PIN or replacing the file ends it. The provider is never
// asked anything: what is not on the disk is downloaded first (0025).

const saveLinkTTL = 48 * time.Hour

var (
	ErrNotOnServer = errors.New("non è ancora un file sul server: prima va scaricato sul server")
	ErrConverting  = errors.New("in conversione, riprova tra poco")
	ErrNotVideo    = errors.New("questo file non è un video che si possa copiare")
	ErrPairedOnly  = errors.New("si copia su un dispositivo abbinato, non con il token di amministrazione")
	// ErrSaveChanged: the file is no longer the one of the link (410).
	ErrSaveChanged = errors.New("il file sul server è cambiato")
)

// saveTypes are the files a device may copy, with their type: the video
// extensions of the collections (library.VideoExt; a test keeps them
// equal). Not mime.TypeByExtension: without /etc/mime.types it knows no
// video, and Debian's makes .ts a Qt translation. ogm and divx get types
// Android maps to no other extension, so a copy keeps its name.
var saveTypes = map[string]string{
	"mkv": "video/x-matroska", "mp4": "video/mp4", "m4v": "video/x-m4v", "avi": "video/x-msvideo",
	"mov": "video/quicktime", "wmv": "video/x-ms-wmv", "ts": "video/mp2t", "m2ts": "video/mp2t",
	"mts": "video/mp2t", "webm": "video/webm", "mpg": "video/mpeg", "mpeg": "video/mpeg",
	"flv": "video/x-flv", "ogm": "video/x-ogm", "divx": "video/divx",
}

// SaveLink is what a device needs to copy a file: url (under /stream, no
// header needed), name with at most one folder, size and etag to resume.
type SaveLink struct {
	URL  string `json:"url"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
	ETag string `json:"etag"`
}

// SaveFile describes the file behind a link, for the HTTP answer.
type SaveFile struct {
	Media   string
	ID      int64
	Name    string // without folders
	MIME    string
	ETag    string
	ModTime time.Time
}

type saveToken struct {
	media                                     string
	id, size, mtime, expires, device, profile int64
}

func (t saveToken) String() string {
	return fmt.Sprintf("%s.%d.%d.%d.%d.%d.%d", t.media, t.id, t.size, t.mtime, t.expires, t.device, t.profile)
}

func (s *Service) saveSignature(t saveToken, paired, pinEpoch int64) string {
	m := hmac.New(sha256.New, s.box.Derive("save-file"))
	fmt.Fprintf(m, "%s|%d|%d", t, paired, pinEpoch)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

func parseSaveToken(raw string) (saveToken, string, bool) {
	f := strings.Split(raw, ".")
	if len(f) != 8 {
		return saveToken{}, "", false
	}
	var n [6]int64
	for i := range n {
		v, err := strconv.ParseInt(f[i+1], 10, 64)
		if err != nil {
			return saveToken{}, "", false
		}
		n[i] = v
	}
	return saveToken{media: f[0], id: n[0], size: n[1], mtime: n[2], expires: n[3], device: n[4], profile: n[5]}, f[7], true
}

func saveETag(fi fs.FileInfo) string {
	return `"` + strconv.FormatInt(fi.Size(), 36) + "-" + strconv.FormatInt(fi.ModTime().UnixNano(), 36) + `"`
}

func fileExt(name string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
}

// SaveLink signs a link to copy a film, an episode or a recording onto
// the device asking, for 48 hours.
func (s *Service) SaveLink(ctx context.Context, id Identity, media string, mediaID int64) (SaveLink, error) {
	dir, err := s.downloadsDir() // unset in the device on its own (0021)
	if err != nil {
		return SaveLink{}, err
	}
	if id.DeviceID == MasterDevice || id.Profile == nil {
		return SaveLink{}, ErrPairedOnly
	}
	file, name, err := s.saveTarget(ctx, dir, media, mediaID)
	if err != nil {
		return SaveLink{}, err
	}
	s.rec.mu.Lock()
	converting := media == "recording" && s.rec.converting[mediaID]
	s.rec.mu.Unlock()
	if converting {
		return SaveLink{}, ErrConverting
	}
	mime, ok := saveTypes[fileExt(file)]
	if !ok {
		return SaveLink{}, ErrNotVideo
	}
	fi, err := os.Stat(file)
	if err != nil || !fi.Mode().IsRegular() {
		return SaveLink{}, ErrNotOnServer
	}
	d, err := s.st.Device(ctx, id.DeviceID)
	if err != nil {
		return SaveLink{}, err
	}
	t := saveToken{media: media, id: mediaID, size: fi.Size(), mtime: fi.ModTime().UnixNano(),
		expires: time.Now().Add(saveLinkTTL).Unix(), device: d.ID, profile: id.Profile.ID}
	return SaveLink{URL: "/stream/save/" + t.String() + "." + s.saveSignature(t, d.CreatedAt, id.Profile.PINEpoch),
		Name: name, MIME: mime, Size: fi.Size(), ETag: saveETag(fi)}, nil
}

// OpenSave opens the file of a link. store.ErrNotFound: the link is forged,
// expired, or its device or profile changed; ErrSaveChanged: the file is
// gone or another one. The SaveFile has media and id even on an error, for
// the log (the token must not go there).
func (s *Service) OpenSave(ctx context.Context, token string) (*os.File, SaveFile, error) {
	t, sig, ok := parseSaveToken(token)
	out := SaveFile{Media: t.media, ID: t.id}
	if !ok || time.Now().Unix() > t.expires {
		return nil, out, store.ErrNotFound
	}
	d, err := s.st.Device(ctx, t.device)
	if err != nil {
		return nil, out, err
	}
	p, err := s.st.Profile(ctx, t.profile)
	if err != nil {
		return nil, out, err
	}
	if !hmac.Equal([]byte(sig), []byte(s.saveSignature(t, d.CreatedAt, p.PINEpoch))) ||
		p.Kind == "kids" || (d.ProfileLock != nil && *d.ProfileLock != p.ID) {
		return nil, out, store.ErrNotFound
	}
	dir, err := s.downloadsDir()
	if err != nil {
		return nil, out, store.ErrNotFound
	}
	file, name, err := s.saveTarget(ctx, dir, t.media, t.id)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrNotOnServer) {
		return nil, out, ErrSaveChanged
	}
	if err != nil {
		return nil, out, err
	}
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, out, ErrSaveChanged
	}
	if err != nil {
		return nil, out, err
	}
	fi, err := f.Stat()
	if err == nil && (fi.Size() != t.size || fi.ModTime().UnixNano() != t.mtime || saveTypes[fileExt(file)] == "") {
		err = ErrSaveChanged
	}
	if err != nil {
		f.Close()
		return nil, out, err
	}
	out.Name, out.MIME, out.ETag, out.ModTime = path.Base(name), saveTypes[fileExt(file)], saveETag(fi), fi.ModTime()
	return f, out, nil
}

// saveTarget finds the file of a film, an episode or a finished recording
// on the server's disk, and its name on the device (the names of 0025):
// "Title (Year).mkv", "Series (Year)/Series S01E02 - Title.mkv",
// "Registrazioni/Title - Channel 2026-11-08 2100.mp4".
func (s *Service) saveTarget(ctx context.Context, dir, media string, id int64) (file, name string, err error) {
	switch media {
	case "movie":
		it, err := s.st.PlaybackItem(ctx, id)
		if err != nil {
			return "", "", err
		}
		if it.Kind != "movie" {
			return "", "", invalid("non è un film")
		}
		b, err := s.st.ItemBasics(ctx, id)
		if err != nil {
			return "", "", err
		}
		name = fileName(withYear(b.Title, b.Year))
		file, err = s.diskFile(ctx, dir, media, id, it)
		if err != nil {
			return "", "", err
		}
	case "episode":
		it, e, err := s.st.EpisodePlayback(ctx, id)
		if err != nil {
			return "", "", err
		}
		series, err := s.st.ItemBasics(ctx, e.SeriesItemID)
		if err != nil {
			return "", "", err
		}
		ep := fmt.Sprintf("%s S%02dE%02d", series.Title, e.Season, e.Number)
		if e.Title != "" {
			ep += " - " + e.Title
		}
		name = fileName(withYear(series.Title, series.Year)) + "/" + fileName(ep)
		file, err = s.diskFile(ctx, dir, media, id, it)
		if err != nil {
			return "", "", err
		}
	case "recording":
		r, err := s.st.Recording(ctx, id)
		if err != nil {
			return "", "", err
		}
		if r.Status != "completed" || r.File == "" {
			return "", "", ErrNotOnServer
		}
		file = r.File
		// The date outside fileName's cut: two airings never share a name.
		name = "Registrazioni/" + fileName(r.Title+" - "+r.Channel) + " " + time.Unix(r.Start, 0).Format("2006-01-02 1504")
	default:
		return "", "", invalid("cosa copiare: movie, episode o recording")
	}
	return file, name + "." + fileExt(file), nil
}

// diskFile is the file of a collection's title, or of a finished download
// of a provider's (kept inside the downloads folder).
func (s *Service) diskFile(ctx context.Context, dir, media string, id int64, it store.PlaybackItem) (string, error) {
	if file, local, err := s.localFile(ctx, it.SubscriptionID, it.SourceID); err != nil || local {
		return file, err
	}
	d, err := s.st.DownloadOf(ctx, media, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.Status != "done") {
		return "", ErrNotOnServer
	}
	if err != nil {
		return "", err
	}
	return library.Resolve(dir, d.Path)
}
