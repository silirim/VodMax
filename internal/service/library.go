package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/silirim/vodmax/internal/catalog"
	"github.com/silirim/vodmax/internal/library"
	"github.com/silirim/vodmax/internal/playlist/xtream"
	"github.com/silirim/vodmax/internal/store"
)

// Local collections are subscriptions of kind "local": a folder of films
// or series on the machine (or a share mounted on it) that VodMax reads as
// it is. They are not a provider: no VPN, no connection limit, and the
// files never leave the server except to the player.

type localSettings struct {
	Path    string `json:"path"`
	Content string `json:"content"` // library.Movies or library.Series
}

func checkLocal(dir, content string) (localSettings, error) {
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		return localSettings{}, invalid("indica il percorso completo della cartella (es. /srv/film)")
	}
	dir = filepath.Clean(dir)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return localSettings{}, invalid("la cartella %s non esiste o il server non la vede (permessi dell'utente del servizio, cartelle nascoste dal servizio)", dir)
	}
	if content != library.Movies && content != library.Series {
		return localSettings{}, invalid("contenuto della raccolta non valido: film o serie")
	}
	return localSettings{Path: dir, Content: content}, nil
}

// local returns the settings of a local collection, or ok=false for a
// provider subscription.
func (s *Service) local(ctx context.Context, subID int64) (localSettings, bool, error) {
	sub, err := s.st.Subscription(ctx, subID)
	if err != nil {
		return localSettings{}, false, err
	}
	if sub.Kind != "local" {
		return localSettings{}, false, nil
	}
	var set localSettings
	if err := json.Unmarshal(sub.Settings, &set); err != nil {
		return set, true, err
	}
	return set, true, nil
}

// syncLocal reads the folder and replaces the collection's catalog and
// episodes.
func (s *Service) syncLocal(ctx context.Context, sub store.Subscription, allowDrop bool) (SyncResult, error) {
	var set localSettings
	json.Unmarshal(sub.Settings, &set)
	res, err := library.Scan(set.Path, set.Content)
	if err != nil {
		return SyncResult{}, err
	}
	// The collection's name is its one category: nothing to classify.
	cats := []store.Category{{Kind: set.Content, SourceID: library.Category, Name: sub.Name}}
	imp, err := s.st.ReplaceCatalog(ctx, sub.ID, []string{set.Content}, cats, res.Items, allowDrop, nil)
	if err != nil {
		return SyncResult{}, err
	}
	if set.Content == library.Series {
		ids, err := s.st.ItemIDs(ctx, sub.ID, "series")
		if err != nil {
			return SyncResult{}, err
		}
		for sourceID, eps := range res.Episodes {
			if id, ok := ids[sourceID]; ok {
				if err := s.st.ReplaceEpisodes(ctx, id, eps); err != nil {
					return SyncResult{}, err
				}
			}
		}
	}
	return SyncResult{ImportResult: imp, Skipped: res.Skipped}, nil
}

// localFile is the file of a film or episode of a local collection.
func (s *Service) localFile(ctx context.Context, subID int64, sourceID string) (string, bool, error) {
	set, ok, err := s.local(ctx, subID)
	if err != nil || !ok {
		return "", ok, err
	}
	f, err := library.Resolve(set.Path, sourceID)
	return f, true, err
}

// openLocal answers like a provider would: the file, or the asked range of
// it (ffmpeg and ffprobe read collections through the loopback relay too).
func openLocal(file, rangeHeader string) (*http.Response, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("file della raccolta non leggibile: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := fi.Size()
	from, to, ranged, err := parseRange(rangeHeader)
	if err != nil || (ranged && from >= size) {
		f.Close()
		return nil, &UpstreamError{Status: http.StatusRequestedRangeNotSatisfiable}
	}
	if to < 0 || to >= size {
		to = size - 1
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, ContentLength: to - from + 1,
		Body: readCloser{io.LimitReader(f, to-from+1), f}}
	resp.Header.Set("Accept-Ranges", "bytes")
	resp.Header.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	if ranged {
		resp.StatusCode = http.StatusPartialContent
		resp.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, size))
	}
	return resp, nil
}

// openLocalDirect serves a direct session of a local file.
func openLocalDirect(file, title, rangeHeader string) (*Stream, error) {
	resp, err := openLocal(file, rangeHeader)
	if err != nil {
		return nil, err
	}
	st := &Stream{ReadCloser: resp.Body, ContentType: "video/mp4", Title: title, Status: resp.StatusCode, ContentLength: resp.ContentLength}
	if resp.StatusCode == http.StatusPartialContent {
		st.ContentRange = resp.Header.Get("Content-Range")
	}
	return st, nil
}

const maxLocalImageBytes = 15 << 20

// localImage reads a picture of a collection ("local:<path>"); pictures
// are read from disk each time, not copied into the cache.
func (s *Service) localImage(ctx context.Context, subID int64, ref string) (store.CachedImage, error) {
	rel, ok := strings.CutPrefix(ref, library.ImageScheme)
	if !ok {
		return store.CachedImage{}, ErrNoImage
	}
	set, ok, err := s.local(ctx, subID)
	if err != nil || !ok {
		return store.CachedImage{}, ErrNoImage
	}
	file, err := library.Resolve(set.Path, rel)
	if err != nil {
		return store.CachedImage{}, ErrNoImage
	}
	f, err := os.Open(file)
	if err != nil {
		return store.CachedImage{}, ErrNoImage
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxLocalImageBytes+1))
	ctype := http.DetectContentType(data)
	if err != nil || len(data) > maxLocalImageBytes || !allowedImageTypes[ctype] {
		return store.CachedImage{}, ErrNoImage
	}
	return store.CachedImage{ContentType: ctype, Data: data, FetchedAt: time.Now()}, nil
}

// localDetails reads a collection title's .nfo as the provider details;
// its backdrop is a "local:" reference.
func (s *Service) localDetails(ctx context.Context, m store.MetaItem) xtream.Details {
	set, ok, err := s.local(ctx, m.SubscriptionID)
	if err != nil || !ok {
		return xtream.Details{}
	}
	n, fanart := library.Details(set.Path, m.Kind, m.SourceID)
	d := xtream.Details{Plot: n.Plot, Cast: strings.Join(n.Actors, ", "), Director: strings.Join(n.Directors, ", "),
		Genre: strings.Join(n.Genres, ", "), Released: n.Premiered, Country: strings.Join(n.Countries, ", "),
		OriginalTitle: n.OriginalTitle, Trailer: n.Trailer, Rating: n.Rating, DurationSecs: n.Runtime * 60, TMDB: n.TMDB}
	if fanart != "" {
		d.Backdrops = []string{library.ImageScheme + fanart}
	}
	return d
}

// ---- Choosing a folder ----

// Folder is a subfolder offered when choosing a collection's folder.
type Folder struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FolderList is the content of a folder of the server, for the folder
// picker of the settings.
type FolderList struct {
	Path    string   `json:"path"`
	Parent  string   `json:"parent,omitempty"`
	Folders []Folder `json:"folders"`
	Videos  int      `json:"videos"` // video files directly inside
}

// Folders lists the subfolders of dir ("" = the root of the file system).
func (s *Service) Folders(dir string) (FolderList, error) {
	if dir == "" {
		dir = string(filepath.Separator)
	}
	if !filepath.IsAbs(dir) {
		return FolderList{}, invalid("percorso non valido")
	}
	dir = filepath.Clean(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FolderList{}, invalid("la cartella %s non esiste", dir)
		}
		return FolderList{}, invalid("la cartella %s non è leggibile dal server", dir)
	}
	out := FolderList{Path: dir, Folders: []Folder{}}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = parent
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(dir, name)); err == nil {
				isDir = fi.IsDir()
			}
		}
		if isDir {
			out.Folders = append(out.Folders, Folder{Name: name, Path: filepath.Join(dir, name)})
		} else if isVideo(name) {
			out.Videos++
		}
	}
	sort.Slice(out.Folders, func(a, b int) bool {
		return strings.ToLower(out.Folders[a].Name) < strings.ToLower(out.Folders[b].Name)
	})
	return out, nil
}

func isVideo(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "mkv", "mp4", "m4v", "avi", "mov", "wmv", "ts", "m2ts", "webm", "mpg", "mpeg":
		return true
	}
	return false
}

// localRules: a collection's titles come first among the variants of a
// title (the file at home before the provider's copy).
func localRules(rules map[int64]catalog.Rules, subs []store.Subscription) {
	for _, sub := range subs {
		if sub.Kind == "local" {
			r := rules[sub.ID]
			r.Prefer = true
			rules[sub.ID] = r
		}
	}
}
