package service

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silirim/vodmax/internal/library"
	"github.com/silirim/vodmax/internal/playlist/xtream"
	"github.com/silirim/vodmax/internal/store"
)

// Downloads copy films and episodes of a subscription to the server's
// disk, one at a time, in folders named like a Jellyfin/Kodi library
// (Movies/…, TV Shows/…) with a .nfo and the poster. Those folders are two
// local collections ("Scaricati"), so a downloaded title plays from the
// disk, without the provider. A download only uses a free connection and
// gives it up to anyone who wants to watch: it resumes later from where
// it stopped.

const (
	downloadTick     = 15 * time.Second
	downloadSettings = "downloads"
	downloadRetry    = 5 * time.Minute // after a network error
	moviesDir        = "Movies"
	showsDir         = "TV Shows"
)

// DownloadSettings: an optional time window (e.g. only at night).
type DownloadSettings struct {
	NightOnly bool `json:"night_only"`
	FromHour  int  `json:"from_hour"` // window start, local hour
	ToHour    int  `json:"to_hour"`   // window end (exclusive)
}

var defaultDownloadSettings = DownloadSettings{FromHour: 1, ToHour: 7}

type downloadState struct {
	mu     sync.Mutex
	dir    string
	log    *slog.Logger
	wake   chan struct{}
	active *activeDownload
	wait   map[int64]time.Time // not before (after an error)
}

type activeDownload struct {
	id     int64
	cancel context.CancelFunc
	bytes  int64
	total  int64
}

// downloadMeta is kept with a queued download, for its .nfo and poster.
type downloadMeta struct {
	Kind        string `json:"kind"` // movie or episode
	Title       string `json:"title"`
	Year        int    `json:"year,omitempty"`
	ItemID      int64  `json:"item_id"` // the film, or the series
	Image       string `json:"image,omitempty"`
	SeriesTitle string `json:"series_title,omitempty"`
	Season      int    `json:"season,omitempty"`
	Episode     int    `json:"episode,omitempty"`
	Plot        string `json:"plot,omitempty"`
}

// StartDownloads runs the queue, saving into dir.
func (s *Service) StartDownloads(ctx context.Context, dir string, log *slog.Logger) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := s.st.RequeueInterrupted(ctx); err != nil {
		return err
	}
	s.dl.mu.Lock()
	s.dl.dir, s.dl.log, s.dl.wake, s.dl.wait = dir, log, make(chan struct{}, 1), map[int64]time.Time{}
	s.dl.mu.Unlock()
	go func() {
		t := time.NewTicker(downloadTick)
		defer t.Stop()
		for {
			s.nextDownload(ctx)
			select {
			case <-ctx.Done():
				s.dl.mu.Lock()
				if a := s.dl.active; a != nil {
					a.cancel()
				}
				s.dl.mu.Unlock()
				return
			case <-t.C:
			case <-s.dl.wake:
			}
		}
	}()
	return nil
}

func (s *Service) wakeDownloads() {
	s.dl.mu.Lock()
	w := s.dl.wake
	s.dl.mu.Unlock()
	if w != nil {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

func (s *Service) downloadsDir() (string, error) {
	s.dl.mu.Lock()
	defer s.dl.mu.Unlock()
	if s.dl.dir == "" {
		return "", invalid("su questo dispositivo non si scarica: i download sono del server di casa")
	}
	return s.dl.dir, nil
}

func (s *Service) DownloadSettings(ctx context.Context) DownloadSettings {
	set := defaultDownloadSettings
	s.st.Setting(ctx, downloadSettings, &set)
	return set
}

func (s *Service) SetDownloadSettings(ctx context.Context, set DownloadSettings) error {
	if set.FromHour < 0 || set.FromHour > 23 || set.ToHour < 0 || set.ToHour > 23 || set.FromHour == set.ToHour {
		return invalid("fascia oraria non valida (ore 0-23, inizio diverso dalla fine)")
	}
	if err := s.st.SetSetting(ctx, downloadSettings, set); err != nil {
		return err
	}
	s.wakeDownloads()
	return nil
}

// inWindow: downloads may run at now.
func (set DownloadSettings) inWindow(now time.Time) bool {
	if !set.NightOnly {
		return true
	}
	h := now.Hour()
	if set.FromHour < set.ToHour {
		return h >= set.FromHour && h < set.ToHour
	}
	return h >= set.FromHour || h < set.ToHour // across midnight
}

// ---- Queueing ----

// DownloadRequest asks for a film, an episode, a season or a whole series.
type DownloadRequest struct {
	Media  string `json:"media"` // movie, episode, season, series
	ID     int64  `json:"id"`    // the film, episode or series
	Season int    `json:"season,omitempty"`
}

// DownloadResult says how many were queued and how many were already there.
type DownloadResult struct {
	Added   int `json:"added"`
	Already int `json:"already"`
}

func (s *Service) AddDownload(ctx context.Context, req DownloadRequest) (DownloadResult, error) {
	if _, err := s.downloadsDir(); err != nil {
		return DownloadResult{}, err
	}
	if viewerOf(ctx).Kids {
		return DownloadResult{}, invalid("i profili bambini non scaricano")
	}
	var list []store.Download
	switch req.Media {
	case "movie":
		d, err := s.movieDownload(ctx, req.ID)
		if err != nil {
			return DownloadResult{}, err
		}
		list = append(list, d)
	case "episode":
		it, e, err := s.st.EpisodePlayback(ctx, req.ID)
		if err != nil {
			return DownloadResult{}, err
		}
		series, err := s.st.ItemBasics(ctx, e.SeriesItemID)
		if err != nil {
			return DownloadResult{}, err
		}
		if err := s.downloadable(ctx, series.SubscriptionID, series.ID); err != nil {
			return DownloadResult{}, err
		}
		list = append(list, episodeDownload(series, it, e))
	case "season", "series":
		series, err := s.st.ItemBasics(ctx, req.ID)
		if err != nil {
			return DownloadResult{}, err
		}
		if series.Kind != "series" {
			return DownloadResult{}, invalid("non è una serie")
		}
		if err := s.downloadable(ctx, series.SubscriptionID, series.ID); err != nil {
			return DownloadResult{}, err
		}
		eps, err := s.Episodes(ctx, series.ID)
		if err != nil {
			return DownloadResult{}, err
		}
		for _, e := range eps {
			if req.Media == "season" && e.Season != req.Season {
				continue
			}
			it, ep, err := s.st.EpisodePlayback(ctx, e.ID)
			if err != nil {
				return DownloadResult{}, err
			}
			list = append(list, episodeDownload(series, it, ep))
		}
		if len(list) == 0 {
			return DownloadResult{}, invalid("nessun episodio da scaricare")
		}
	default:
		return DownloadResult{}, invalid("cosa scaricare: movie, episode, season o series")
	}
	var res DownloadResult
	for _, d := range list {
		_, added, err := s.st.AddDownload(ctx, d)
		if err != nil {
			return res, err
		}
		if added {
			res.Added++
		} else {
			res.Already++
		}
	}
	s.wakeDownloads()
	return res, nil
}

// downloadable: a provider title the viewer may see (collections are
// already on disk).
func (s *Service) downloadable(ctx context.Context, subID, itemID int64) error {
	if _, local, err := s.local(ctx, subID); err != nil {
		return err
	} else if local {
		return invalid("è già un file della raccolta")
	}
	if sub, err := s.st.Subscription(ctx, subID); err != nil {
		return err
	} else if sub.Kind != "xtream" {
		return invalid("si scaricano per ora solo i titoli degli abbonamenti Xtream")
	}
	return s.allowItem(ctx, itemID)
}

func (s *Service) movieDownload(ctx context.Context, id int64) (store.Download, error) {
	b, err := s.st.ItemBasics(ctx, id)
	if err != nil {
		return store.Download{}, err
	}
	if b.Kind != "movie" {
		return store.Download{}, invalid("non è un film")
	}
	if err := s.downloadable(ctx, b.SubscriptionID, id); err != nil {
		return store.Download{}, err
	}
	it, err := s.st.PlaybackItem(ctx, id)
	if err != nil {
		return store.Download{}, err
	}
	name := fileName(withYear(b.Title, b.Year))
	ext := extOr(it.Extension)
	meta, _ := json.Marshal(downloadMeta{Kind: "movie", Title: b.Title, Year: b.Year, ItemID: id, Image: b.Image})
	return store.Download{Media: "movie", MediaID: id, SubscriptionID: b.SubscriptionID, SourceID: it.SourceID, Extension: ext,
		Title: withYear(b.Title, b.Year), Path: path.Join(moviesDir, name, name+"."+ext), Meta: string(meta)}, nil
}

func episodeDownload(series store.ItemBasics, it store.PlaybackItem, e store.Episode) store.Download {
	show := fileName(withYear(series.Title, series.Year))
	code := fmt.Sprintf("S%02dE%02d", e.Season, e.Number)
	ext := extOr(it.Extension)
	file := fileName(series.Title + " " + code + " - " + e.Title)
	meta, _ := json.Marshal(downloadMeta{Kind: "episode", Title: e.Title, ItemID: series.ID, Image: series.Image, Year: series.Year,
		SeriesTitle: series.Title, Season: e.Season, Episode: e.Number, Plot: e.Plot})
	return store.Download{Media: "episode", MediaID: e.ID, SubscriptionID: series.SubscriptionID, SourceID: it.SourceID, Extension: ext,
		Title: fmt.Sprintf("%s · S%dE%d %s", series.Title, e.Season, e.Number, e.Title),
		Path:  path.Join(showsDir, show, fmt.Sprintf("Season %d", e.Season), file+"."+ext), Meta: string(meta)}
}

func withYear(title string, year int) string {
	if year > 0 {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
}

func extOr(ext string) string {
	ext = strings.ToLower(strings.Trim(ext, ". "))
	if ext == "" || strings.ContainsAny(ext, `/\`) || len(ext) > 5 {
		return "mkv"
	}
	return ext
}

// fileName makes a title safe as a file or folder name on every system.
func fileName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r < 32, strings.ContainsRune(`<>:"/\|?*`, r):
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, ". ")
	if len(s) > 120 {
		s = strings.TrimSpace(s[:120])
	}
	if s == "" {
		s = "VodMax"
	}
	return s
}

// ---- The list ----

func (s *Service) Downloads(ctx context.Context) ([]store.Download, error) {
	if _, err := s.downloadsDir(); err != nil {
		return nil, err
	}
	list, err := s.st.Downloads(ctx)
	if err != nil {
		return nil, err
	}
	s.dl.mu.Lock()
	if a := s.dl.active; a != nil {
		for i := range list {
			if list[i].ID == a.id {
				list[i].Bytes, list[i].Total = a.bytes, a.total
			}
		}
	}
	s.dl.mu.Unlock()
	return list, nil
}

// PauseDownload stops a download (keeping what was downloaded), Resume
// puts it back in the queue.
func (s *Service) PauseDownload(ctx context.Context, id int64) error {
	d, err := s.st.Download(ctx, id)
	if err != nil {
		return err
	}
	if d.Status == "done" {
		return invalid("download già completato")
	}
	s.stopActive(id)
	return s.st.SetDownloadState(ctx, id, "paused", d.Bytes, d.Total, "")
}

func (s *Service) ResumeDownload(ctx context.Context, id int64) error {
	d, err := s.st.Download(ctx, id)
	if err != nil {
		return err
	}
	if d.Status == "done" {
		return invalid("download già completato")
	}
	s.dl.mu.Lock()
	delete(s.dl.wait, id)
	s.dl.mu.Unlock()
	if err := s.st.SetDownloadState(ctx, id, "queued", d.Bytes, d.Total, ""); err != nil {
		return err
	}
	s.wakeDownloads()
	return nil
}

// DeleteDownload removes a download and its files (the partial one too).
func (s *Service) DeleteDownload(ctx context.Context, id int64) error {
	d, err := s.st.Download(ctx, id)
	if err != nil {
		return err
	}
	dir, err := s.downloadsDir()
	if err != nil {
		return err
	}
	s.stopActive(id)
	file, err := library.Resolve(dir, d.Path)
	if err != nil {
		return err
	}
	os.Remove(file + ".part")
	os.Remove(file)
	base := strings.TrimSuffix(file, filepath.Ext(file))
	for _, x := range []string{".nfo", "-thumb.jpg", "-thumb.png", "-thumb.webp", "-thumb.gif"} {
		os.Remove(base + x)
	}
	// Empty folders go too (the film's, a season's, a series').
	for p := filepath.Dir(file); p != dir && strings.HasPrefix(p, dir); p = filepath.Dir(p) {
		entries, err := os.ReadDir(p)
		if err != nil {
			break
		}
		onlyArt := true
		for _, e := range entries {
			n := strings.ToLower(e.Name())
			if e.IsDir() || !(n == "poster.jpg" || n == "poster.png" || n == "fanart.jpg" || n == "tvshow.nfo") {
				onlyArt = false
			}
		}
		if !onlyArt {
			break
		}
		os.RemoveAll(p)
	}
	if err := s.st.DeleteDownload(ctx, id); err != nil {
		return err
	}
	if d.Status == "done" {
		s.syncDownloads(ctx, d.Media, true)
	}
	return nil
}

func (s *Service) stopActive(id int64) {
	s.dl.mu.Lock()
	defer s.dl.mu.Unlock()
	if a := s.dl.active; a != nil && a.id == id {
		a.cancel()
	}
}

// ---- Running ----

func (s *Service) nextDownload(ctx context.Context) {
	s.dl.mu.Lock()
	busy := s.dl.active != nil
	skip := map[int64]bool{}
	now := time.Now()
	for id, t := range s.dl.wait {
		if now.Before(t) {
			skip[id] = true
		}
	}
	s.dl.mu.Unlock()
	if busy || !s.DownloadSettings(ctx).inWindow(now) {
		return
	}
	d, ok, err := s.st.NextDownload(ctx, skip)
	if err != nil || !ok {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	a := &activeDownload{id: d.ID, cancel: cancel, bytes: d.Bytes, total: d.Total}
	s.dl.mu.Lock()
	s.dl.active = a
	s.dl.mu.Unlock()
	go func() {
		defer cancel()
		s.runDownload(runCtx, d, a)
		s.dl.mu.Lock()
		s.dl.active = nil
		s.dl.mu.Unlock()
		s.wakeDownloads()
	}()
}

func (s *Service) runDownload(ctx context.Context, d store.Download, a *activeDownload) {
	dir, _ := s.downloadsDir()
	log := s.dl.log
	file, err := library.Resolve(dir, d.Path)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(file), 0o755)
	}
	if err != nil {
		s.st.SetDownloadState(context.Background(), d.ID, "failed", d.Bytes, d.Total, err.Error())
		return
	}
	if free, _, ok := diskSpace(dir); ok && free < minFreeToRecord+max(0, d.Total-d.Bytes) {
		s.st.SetDownloadState(context.Background(), d.ID, "failed", d.Bytes, d.Total, tr("it", "spazio su disco insufficiente"))
		return
	}
	streamCtx, release, err := s.acquireBackground(ctx, d.SubscriptionID, "download:"+strconv.FormatInt(d.ID, 10))
	if err != nil {
		// Someone is watching (or recording): later.
		s.st.SetDownloadState(context.Background(), d.ID, "queued", d.Bytes, d.Total, tr("it", "in attesa di una connessione libera"))
		s.delay(d.ID, time.Minute)
		return
	}
	defer release()
	s.st.SetDownloadState(context.Background(), d.ID, "downloading", d.Bytes, d.Total, "")
	err = s.fetchDownload(streamCtx, d, file, a)
	bytes, total := s.progress(a)
	switch {
	case err == nil:
		s.finishDownload(context.Background(), d, file)
		s.st.SetDownloadState(context.Background(), d.ID, "done", bytes, total, "")
		log.Info("download completato", "titolo", d.Title)
		s.syncDownloads(context.Background(), d.Media, false)
	case ctx.Err() != nil:
		// Paused or deleted by the user: the state is theirs.
	case streamCtx.Err() != nil:
		s.st.SetDownloadState(context.Background(), d.ID, "queued", bytes, total, tr("it", "in pausa: la connessione serve a chi guarda"))
		s.delay(d.ID, time.Minute)
	default:
		var up *UpstreamError
		if errors.As(err, &up) && up.Status == http.StatusNotFound {
			s.st.SetDownloadState(context.Background(), d.ID, "failed", bytes, total, err.Error())
			return
		}
		s.st.SetDownloadState(context.Background(), d.ID, "queued", bytes, total, err.Error())
		s.delay(d.ID, downloadRetry)
		log.Warn("download interrotto", "titolo", d.Title, "err", err)
	}
}

func (s *Service) delay(id int64, d time.Duration) {
	s.dl.mu.Lock()
	s.dl.wait[id] = time.Now().Add(d)
	s.dl.mu.Unlock()
}

func (s *Service) progress(a *activeDownload) (int64, int64) {
	s.dl.mu.Lock()
	defer s.dl.mu.Unlock()
	return a.bytes, a.total
}

// fetchDownload appends to file.part from where it stopped, then renames it.
func (s *Service) fetchDownload(ctx context.Context, d store.Download, file string, a *activeDownload) error {
	client, err := s.xtreamClient(ctx, d.SubscriptionID, 0)
	if err != nil {
		return err
	}
	kind := xtream.KindMovie
	if d.Media == "episode" {
		kind = xtream.KindEpisode
	}
	url := client.StreamURL(kind, d.SourceID, d.Extension)
	part := file + ".part"
	var have int64
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	rangeHeader := ""
	if have > 0 {
		rangeHeader = fmt.Sprintf("bytes=%d-", have)
	}
	resp, err := s.upstream(ctx, d.SubscriptionID, url, rangeHeader)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	total := resp.ContentLength
	if resp.StatusCode == http.StatusPartialContent {
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndex(cr, "/"); i >= 0 {
				if n, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
					total = n
				}
			}
		}
	} else {
		// The provider sent the whole file: start again.
		have, flags = 0, os.O_CREATE|os.O_WRONLY|os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	s.dl.mu.Lock()
	a.bytes, a.total = have, total
	s.dl.mu.Unlock()
	buf := make([]byte, 256<<10)
	last := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			s.dl.mu.Lock()
			a.bytes += int64(n)
			s.dl.mu.Unlock()
			if time.Since(last) > 5*time.Second {
				last = time.Now()
				b, t := s.progress(a)
				s.st.SetDownloadState(context.Background(), d.ID, "downloading", b, t, "")
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if b, t := s.progress(a); t > 0 && b < t {
		return fmt.Errorf("il provider ha chiuso il file a %d byte su %d", b, t)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, file)
}

// finishDownload writes the .nfo and the poster next to the file, as a
// collection expects them.
func (s *Service) finishDownload(ctx context.Context, d store.Download, file string) {
	var m downloadMeta
	json.Unmarshal([]byte(d.Meta), &m)
	var details xtream.Details
	if mi, err := s.st.MetaItem(ctx, m.ItemID); err == nil {
		details = s.providerDetails(ctx, mi)
	}
	folder := filepath.Dir(file)
	if m.Kind == "episode" {
		folder = filepath.Dir(folder) // the series folder, above "Season N"
		type ep struct {
			XMLName xml.Name `xml:"episodedetails"`
			Title   string   `xml:"title"`
			Season  int      `xml:"season"`
			Episode int      `xml:"episode"`
			Plot    string   `xml:"plot,omitempty"`
		}
		writeXML(strings.TrimSuffix(file, filepath.Ext(file))+".nfo", ep{Title: m.Title, Season: m.Season, Episode: m.Episode, Plot: m.Plot})
		// The episode's picture, as Kodi and Jellyfin name it.
		if img, err := s.EpisodeThumb(ctx, d.MediaID, s.signImage("episode:"+strconv.FormatInt(d.MediaID, 10))); err == nil && len(img.Data) > 0 {
			ext := map[string]string{"image/png": ".png", "image/webp": ".webp", "image/gif": ".gif"}[img.ContentType]
			if ext == "" {
				ext = ".jpg"
			}
			os.WriteFile(strings.TrimSuffix(file, filepath.Ext(file))+"-thumb"+ext, img.Data, 0o644)
		}
		if _, err := os.Stat(filepath.Join(folder, "tvshow.nfo")); err != nil {
			type show struct {
				XMLName xml.Name `xml:"tvshow"`
				Title   string   `xml:"title"`
				Year    int      `xml:"year,omitempty"`
				Plot    string   `xml:"plot,omitempty"`
				Genre   string   `xml:"genre,omitempty"`
				TMDB    string   `xml:"tmdbid,omitempty"`
			}
			writeXML(filepath.Join(folder, "tvshow.nfo"), show{Title: m.SeriesTitle, Year: m.Year, Plot: details.Plot, Genre: details.Genre, TMDB: details.TMDB})
		}
	} else {
		type movie struct {
			XMLName  xml.Name `xml:"movie"`
			Title    string   `xml:"title"`
			Year     int      `xml:"year,omitempty"`
			Plot     string   `xml:"plot,omitempty"`
			Director string   `xml:"director,omitempty"`
			Genre    string   `xml:"genre,omitempty"`
			Country  string   `xml:"country,omitempty"`
			Rating   float64  `xml:"rating,omitempty"`
			Runtime  int      `xml:"runtime,omitempty"`
			TMDB     string   `xml:"tmdbid,omitempty"`
		}
		writeXML(strings.TrimSuffix(file, filepath.Ext(file))+".nfo", movie{Title: m.Title, Year: m.Year, Plot: details.Plot,
			Director: details.Director, Genre: details.Genre, Country: details.Country, Rating: details.Rating,
			Runtime: details.DurationSecs / 60, TMDB: details.TMDB})
	}
	poster := filepath.Join(folder, "poster.jpg")
	if _, err := os.Stat(poster); err == nil || m.ItemID == 0 {
		return
	}
	if img, err := s.Logo(ctx, m.ItemID, s.imageSignature(m.ItemID)); err == nil && len(img.Data) > 0 {
		name := poster
		if img.ContentType == "image/png" {
			name = filepath.Join(folder, "poster.png")
		}
		os.WriteFile(name, img.Data, 0o644)
	}
}

func writeXML(file string, v any) {
	data, err := xml.MarshalIndent(v, "", "  ")
	if err == nil {
		os.WriteFile(file, append([]byte(xml.Header), append(data, '\n')...), 0o644)
	}
}

// syncDownloads keeps the "Scaricati" collections: created with the first
// download, scanned again after each change.
func (s *Service) syncDownloads(ctx context.Context, media string, allowDrop bool) {
	dir, err := s.downloadsDir()
	if err != nil {
		return
	}
	content, sub, name := library.Movies, moviesDir, "Scaricati · Film"
	if media == "episode" {
		content, sub, name = library.Series, showsDir, "Scaricati · Serie"
	}
	folder := filepath.Join(dir, sub)
	os.MkdirAll(folder, 0o755)
	subs, err := s.st.Subscriptions(ctx)
	if err != nil {
		return
	}
	var id, moved int64
	taken := false
	for _, x := range subs {
		var set localSettings
		if x.Kind != "local" || json.Unmarshal(x.Settings, &set) != nil {
			continue
		}
		if set.Path == folder {
			id = x.ID
		}
		if x.Name == name {
			taken = true
			// The same collection with the folder of another data directory
			// (moved, or restored from a backup of another server).
			downloadsLike := filepath.Base(set.Path) == sub && filepath.Base(filepath.Dir(set.Path)) == filepath.Base(dir)
			if fi, err := os.Stat(set.Path); err != nil || !fi.IsDir() || downloadsLike {
				moved = x.ID
			}
		}
	}
	if id == 0 && moved != 0 {
		if err := s.UpdateSubscription(ctx, moved, SubscriptionPatch{Path: &folder}); err == nil {
			id = moved
		} else {
			s.dl.log.Warn("raccolta dei download non aggiornata", "err", err)
		}
	}
	if id == 0 {
		if taken {
			name += " (VodMax)" // the name of a collection made by hand
		}
		if id, err = s.AddSubscription(ctx, SubscriptionInput{Name: name, Kind: "local", Path: folder, Content: content}); err != nil {
			s.dl.log.Warn("raccolta dei download non creata", "err", err)
			return
		}
	}
	if _, err := s.Sync(ctx, id, allowDrop); err != nil && !errors.Is(err, ErrBusy) {
		s.dl.log.Warn("raccolta dei download non aggiornata", "err", err)
	}
}
