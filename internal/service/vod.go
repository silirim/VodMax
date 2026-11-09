package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silirim/vodmax/internal/playlist/xtream"
	"github.com/silirim/vodmax/internal/store"
)

// ---- Series episodes ----

const episodesFreshFor = 12 * time.Hour

// Episodes returns a series' episodes, loading them from the provider
// (through the VPN) when missing or older than episodesFreshFor.
func (s *Service) Episodes(ctx context.Context, seriesItemID int64) ([]store.Episode, error) {
	it, err := s.st.PlaybackItem(ctx, seriesItemID)
	if err != nil {
		return nil, err
	}
	if it.Kind != "series" {
		return nil, invalid("non è una serie")
	}
	if err := s.allowItem(ctx, seriesItemID); err != nil {
		return nil, err
	}
	profile := viewerOf(ctx).ProfileID
	fetched, err := s.st.SeriesFetchedAt(ctx, seriesItemID)
	if err != nil {
		return nil, err
	}
	_, local, err := s.local(ctx, it.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if !local && time.Since(fetched) > episodesFreshFor {
		if err := s.loadEpisodes(ctx, it); err != nil {
			// Stale episodes are better than none.
			if eps, serr := s.st.Episodes(ctx, profile, seriesItemID); serr == nil && len(eps) > 0 {
				return s.withThumbs(eps), nil
			}
			return nil, err
		}
	}
	eps, err := s.st.Episodes(ctx, profile, seriesItemID)
	return s.withThumbs(eps), err
}

func (s *Service) withThumbs(eps []store.Episode) []store.Episode {
	for i := range eps {
		if eps[i].Image != "" {
			eps[i].Thumb = s.EpisodeThumbURL(eps[i].ID)
		}
	}
	return eps
}

func (s *Service) loadEpisodes(ctx context.Context, it store.PlaybackItem) error {
	client, err := s.xtreamClient(ctx, it.SubscriptionID, time.Minute)
	if err != nil {
		return err
	}
	info, err := client.SeriesInfo(ctx, it.SourceID)
	if err != nil {
		return err
	}
	eps := make([]store.Episode, 0, len(info.Episodes))
	for _, e := range info.Episodes {
		if e.ID == "" {
			continue
		}
		title := episodeTitle(e.Title.String())
		eps = append(eps, store.Episode{SourceID: e.ID.String(), Season: int(e.Season.Int()), Number: int(e.EpisodeNum.Int()),
			Title: title, Extension: e.ContainerExtension.String(), Duration: int(e.Info.DurationSecs.Int()),
			Plot: strings.TrimSpace(e.Info.Plot.String()), Image: strings.TrimSpace(e.Info.Image.String())})
	}
	return s.st.ReplaceEpisodes(ctx, it.ID, eps)
}

// Providers often name episodes "Series (year) - S01E01 - Title": keep
// only the title.
var episodeCode = regexp.MustCompile(`(?i)\bS\d{1,3}\s*E\d{1,4}\b\s*[-–:]?\s*`)

func episodeTitle(raw string) string {
	raw = strings.TrimSpace(raw)
	if loc := episodeCode.FindStringIndex(raw); loc != nil {
		if rest := strings.TrimSpace(raw[loc[1]:]); rest != "" {
			return rest
		}
	}
	return raw
}

// ---- Progress ----

func (s *Service) SaveProgress(ctx context.Context, media string, id int64, position, duration int) (store.Progress, error) {
	switch media {
	case "movie":
		it, err := s.st.PlaybackItem(ctx, id)
		if err != nil {
			return store.Progress{}, err
		}
		if it.Kind != "movie" {
			return store.Progress{}, invalid("non è un film")
		}
		if err := s.allowItem(ctx, id); err != nil {
			return store.Progress{}, err
		}
	case "episode":
		_, e, err := s.st.EpisodePlayback(ctx, id)
		if err != nil {
			return store.Progress{}, err
		}
		if err := s.allowItem(ctx, e.SeriesItemID); err != nil {
			return store.Progress{}, err
		}
	default:
		return store.Progress{}, invalid("tipo non valido: movie o episode")
	}
	p, err := s.st.SaveProgress(ctx, viewerOf(ctx).ProfileID, media, id, position, duration)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return p, invalid("%v", err)
	}
	return p, err
}

// Progress returns the saved position of a film or episode (nil if none).
func (s *Service) Progress(ctx context.Context, media string, id int64) (*store.Progress, error) {
	if media != "movie" && media != "episode" {
		return nil, invalid("tipo non valido: movie o episode")
	}
	return s.st.GetProgress(ctx, viewerOf(ctx).ProfileID, media, id)
}

func (s *Service) Continue(ctx context.Context) ([]store.ContinueEntry, error) {
	v := viewerOf(ctx)
	list, err := s.st.Continue(ctx, v.ProfileID, v.Kids, 30)
	for i := range list {
		if list[i].ImageAllowed {
			list[i].Logo = s.LogoURL(list[i].ImageItemID)
		}
	}
	return list, err
}

// HideContinue takes a film or a series off the viewer's "continue
// watching" until they watch it again.
func (s *Service) HideContinue(ctx context.Context, itemID int64) error {
	return s.st.HideContinue(ctx, viewerOf(ctx).ProfileID, itemID)
}

// ---- Playback sessions ----

// A playback session covers one film or episode being watched by one
// viewer. "direct" sessions relay the provider's file as is, with Range
// requests (MP4 with H.264: browsers play it natively and can seek). Other
// files are remuxed by ffmpeg into fragmented MP4 from a given position
// ("remux"); ffmpeg reads them through a loopback-only relay, so it never
// reaches the provider except through the VPN.

type vodSession struct {
	id       string
	viewer   string
	media    string
	mediaID  int64
	subID    int64
	url      string // provider URL with credentials: never leaves the server
	title    string
	mode     string
	probe    *probeResult
	expires  time.Time
	internal string // loopback token for ffmpeg
	subDir   string // temporary folder of extracted subtitles
	subFile  string // WebVTT being written by the current remux
	catchup  *catchupInfo
	cache    *rangeCache // direct mode: the single provider connection
	forced   string      // direct mode: the last "take control" value honoured
	local    bool        // url is a file of a local collection
}

// addVODSession registers a session, replacing the viewer's previous one.
func (s *Service) addVODSession(sess *vodSession) {
	s.vod.mu.Lock()
	defer s.vod.mu.Unlock()
	if s.vod.sessions == nil {
		s.vod.sessions, s.vod.byToken = map[string]*vodSession{}, map[string]*vodSession{}
	}
	for k, old := range s.vod.sessions {
		if old.viewer == sess.viewer || time.Now().After(old.expires) {
			delete(s.vod.sessions, k)
			delete(s.vod.byToken, old.internal)
			old.drop()
		}
	}
	s.vod.sessions[sess.id] = sess
	s.vod.byToken[sess.internal] = sess
}

// drop removes what a session left on disk.
func (v *vodSession) drop() {
	if v.subDir != "" {
		os.RemoveAll(v.subDir)
	}
	if v.cache != nil {
		v.cache.Close()
	}
}

type vodState struct {
	mu       sync.Mutex
	sessions map[string]*vodSession
	byToken  map[string]*vodSession
	loopback string // "127.0.0.1:port" of the ffmpeg relay
	cacheDir string // chunks of direct playback
	once     sync.Once
	ffmpeg   string
	ffprobe  string
	// pacedRemux's answer for the ffmpeg in pacedBin ("" until known).
	pacedBin string
	paced    bool
}

// SetFFmpeg sets the ffmpeg and ffprobe executables (default: from PATH).
func (s *Service) SetFFmpeg(ffmpeg, ffprobe string) {
	s.vod.ffmpeg, s.vod.ffprobe = ffmpeg, ffprobe
	s.vod.mu.Lock()
	s.vod.pacedBin = ""
	s.vod.mu.Unlock()
}

// PlaybackSession is what a client needs to play.
type PlaybackSession struct {
	ID       string          `json:"id"`
	Mode     string          `json:"mode"` // direct or remux
	URL      string          `json:"url"`
	Title    string          `json:"title"`
	Duration int             `json:"duration,omitempty"` // seconds, when known
	Resume   *store.Progress `json:"resume,omitempty"`
	// Tracks found in the file (empty if it could not be analysed), and
	// the suggested choice: Italian audio when present, no subtitles.
	Audio        []Track `json:"audio"`
	Subtitles    []Track `json:"subtitles"`
	DefaultAudio int     `json:"default_audio"`
	// SubtitlesURL returns the WebVTT of the chosen subtitles, written
	// while the remux runs (?sub=N on the remux URL).
	SubtitlesURL string `json:"subtitles_url,omitempty"`
	// TS: the remux URL also sends MPEG-TS at playing speed (?ts=1), as
	// the browser plays a catch-up; it needs FFmpeg 6.1 or later.
	TS bool `json:"ts,omitempty"`
	// Episodes: the one after, and where opening and credits are.
	Next    *NextEpisode         `json:"next,omitempty"`
	Markers *store.SeriesMarkers `json:"markers,omitempty"`
	Series  int64                `json:"series_id,omitempty"`
}

// NextEpisode is the episode that follows the one playing.
type NextEpisode struct {
	ID     int64  `json:"id"`
	Season int    `json:"season"`
	Number int    `json:"number"`
	Title  string `json:"title"` // as the player shows it: "Series · S1E2 Title"
}

// Track is an audio or subtitle stream; Index counts within its type.
type Track struct {
	Index     int    `json:"index"`
	Language  string `json:"language,omitempty"`
	Title     string `json:"title,omitempty"`
	Codec     string `json:"codec"`
	Channels  int    `json:"channels,omitempty"`
	Default   bool   `json:"default,omitempty"`
	Forced    bool   `json:"forced,omitempty"`
	Supported bool   `json:"supported"` // subtitles: text, so the browser can show them
}

// NewPlaybackSession prepares a film or episode for a viewer. mode may
// force "remux" (e.g. after the browser failed to play the direct file).
// force takes the connection from another viewer for the analysis ("take
// control"); without it a busy connection is ConnectionBusy.
func (s *Service) NewPlaybackSession(ctx context.Context, media string, id int64, viewer, mode string, force bool) (PlaybackSession, error) {
	if !viewerID.MatchString(viewer) {
		return PlaybackSession{}, invalid("identificativo del player non valido")
	}
	s.stopViewerTimeshift(viewer)
	var it store.PlaybackItem
	var duration int
	var episode store.Episode
	title := ""
	kind := xtream.KindMovie
	switch media {
	case "movie":
		var err error
		if it, err = s.st.PlaybackItem(ctx, id); err != nil {
			return PlaybackSession{}, err
		}
		if it.Kind != "movie" {
			return PlaybackSession{}, invalid("non è un film")
		}
		if err := s.allowItem(ctx, id); err != nil {
			return PlaybackSession{}, err
		}
		title = it.Title
	case "episode":
		var e store.Episode
		var err error
		if it, e, err = s.st.EpisodePlayback(ctx, id); err != nil {
			return PlaybackSession{}, err
		}
		if err := s.allowItem(ctx, e.SeriesItemID); err != nil {
			return PlaybackSession{}, err
		}
		kind, duration, episode = xtream.KindEpisode, e.Duration, e
		title = playerTitle(it.Title, e)
	default:
		return PlaybackSession{}, invalid("tipo non valido: movie o episode")
	}
	ext := strings.ToLower(it.Extension)
	sourceID := it.SourceID // the episode's for an episode
	var url string
	file, local, err := s.localFile(ctx, it.SubscriptionID, sourceID)
	if err != nil {
		return PlaybackSession{}, err
	}
	if local {
		url = file
	} else {
		client, err := s.xtreamClient(ctx, it.SubscriptionID, time.Minute)
		if err != nil {
			return PlaybackSession{}, err
		}
		url = client.StreamURL(kind, sourceID, ext)
	}
	auto := mode == ""
	if mode == "" {
		mode = "remux"
		if ext == "mp4" || ext == "m4v" {
			mode = "direct"
		}
	}
	if mode != "direct" && mode != "remux" {
		return PlaybackSession{}, invalid("modalità non valida")
	}
	sess := &vodSession{id: randomToken(), viewer: viewer, media: media, mediaID: id, subID: it.SubscriptionID,
		url: url, local: local, title: title, mode: mode, internal: randomToken(),
		expires: time.Now().Add(8 * time.Hour)}
	s.addVODSession(sess)

	out := PlaybackSession{ID: sess.id, Mode: mode, Title: title, Duration: duration, URL: "/stream/vod/" + sess.id,
		Audio: []Track{}, Subtitles: []Track{}}
	// The analysis lists the tracks; a direct file can still play without
	// it, so it does not wait long (an MP4 with its index at the end needs
	// several requests).
	probeCtx, cancel := ctx, context.CancelFunc(func() {})
	if mode == "direct" {
		probeCtx, cancel = context.WithTimeout(ctx, directProbeLimit)
	}
	probe, err := s.probe(probeCtx, sess, force)
	cancel()
	// A direct file can play without the analysis, but a busy connection
	// is reported now, also to the Android player that asks for direct:
	// taking control here also lets the analysis pick the Italian audio.
	if err != nil && (mode == "remux" || errors.As(err, new(ConnectionBusy))) {
		return PlaybackSession{}, err
	}
	if err == nil {
		sess.probe = probe
		if probe.Duration > 0 {
			out.Duration = int(probe.Duration)
		}
		out.Audio, out.Subtitles, out.DefaultAudio = probe.Audio, probe.Subtitles, probe.preferredAudio()
		out.SubtitlesURL = "/stream/vod/" + sess.id + "/subtitles"
		// A browser plays the first audio track of a direct file: when
		// the preferred one is another, convert with it.
		if mode == "direct" && auto && out.DefaultAudio != 0 {
			mode, sess.mode, out.Mode = "remux", "remux", "remux"
		}
	}
	if mode == "remux" {
		out.URL += "/remux"
	}
	if p, err := s.st.GetProgress(ctx, viewerOf(ctx).ProfileID, media, id); err == nil && p != nil && !p.Completed && p.Position > 30 {
		out.Resume = p
	}
	if media == "episode" {
		out.Series = episode.SeriesItemID
		if next, err := s.nextEpisode(ctx, it.Title, episode); err == nil {
			out.Next = next
		}
		if m, err := s.st.SeriesMarkers(ctx, episode.SeriesItemID); err == nil && (m.IntroEnd > 0 || m.Credits > 0) {
			out.Markers = &m
		}
	}
	return out, nil
}

func playerTitle(series string, e store.Episode) string {
	return fmt.Sprintf("%s · S%dE%d %s", series, e.Season, e.Number, e.Title)
}

// nextEpisode is the episode after e in its series, nil after the last.
func (s *Service) nextEpisode(ctx context.Context, series string, e store.Episode) (*NextEpisode, error) {
	list, err := s.st.Episodes(ctx, viewerOf(ctx).ProfileID, e.SeriesItemID)
	if err != nil {
		return nil, err
	}
	for i, x := range list {
		if x.ID == e.ID && i+1 < len(list) {
			n := list[i+1]
			return &NextEpisode{ID: n.ID, Season: n.Season, Number: n.Number, Title: playerTitle(series, n)}, nil
		}
	}
	return nil, nil
}

// SetSeriesMarkers records where a series' opening ends or how long its
// end credits are, as someone watching showed it.
func (s *Service) SetSeriesMarkers(ctx context.Context, seriesItemID int64, m store.SeriesMarkers) error {
	switch {
	case m.IntroEnd != 0 && (m.IntroStart < 0 || m.IntroEnd <= m.IntroStart || m.IntroEnd > 600 || m.IntroEnd-m.IntroStart < 10):
		return invalid("sigla non valida")
	case m.Credits < 0 || m.Credits > 900:
		return invalid("titoli di coda non validi")
	case m.IntroEnd == 0 && m.Credits == 0:
		return invalid("niente da salvare")
	}
	if err := s.allowItem(ctx, seriesItemID); err != nil {
		return err
	}
	return s.st.SetSeriesMarkers(ctx, seriesItemID, m)
}

// ErrSession means the playback session is unknown or expired.
var ErrSession = errors.New("sessione di riproduzione non valida o scaduta")

func (s *Service) session(id string) (*vodSession, error) {
	s.vod.mu.Lock()
	defer s.vod.mu.Unlock()
	sess := s.vod.sessions[id]
	if sess == nil || time.Now().After(sess.expires) {
		return nil, ErrSession
	}
	return sess, nil
}

// OpenDirect relays (a range of) the provider file. Each request replaces
// the viewer's previous stream: browsers abandon the old request when
// seeking.
// force is the value of one "take control" (a new one per confirmation):
// it is honoured once, so the player's later ranges and retries of the
// same address ask like anyone else.
func (s *Service) OpenDirect(ctx context.Context, sessionID, rangeHeader, force string) (*Stream, error) {
	sess, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	if sess.mode != "direct" {
		return nil, invalid("sessione non diretta")
	}
	if sess.local {
		return openLocalDirect(sess.url, sess.title, rangeHeader)
	}
	from, to, ranged, err := parseRange(rangeHeader)
	if err != nil {
		return nil, err
	}
	cache, err := s.directCache(sess, force)
	if err != nil {
		return nil, err
	}
	size, err := cache.Size(ctx, from)
	if err != nil {
		return nil, err
	}
	if from >= size {
		return nil, invalid("intervallo oltre la fine del file")
	}
	if to < 0 || to >= size {
		to = size - 1
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(cache.WriteRange(ctx, pw, from, to)) }()
	st := &Stream{ReadCloser: pr, ContentType: "video/mp4", Title: sess.title, Status: http.StatusOK, ContentLength: to - from + 1}
	if ranged {
		st.Status, st.ContentRange = http.StatusPartialContent, fmt.Sprintf("bytes %d-%d/%d", from, to, size)
	}
	return st, nil
}

// parseRange reads "bytes=a-b" or "bytes=a-" (to = -1); no header is the
// whole file.
func parseRange(h string) (from, to int64, ranged bool, err error) {
	if h == "" {
		return 0, -1, false, nil
	}
	spec, ok := strings.CutPrefix(h, "bytes=")
	a, b, ok2 := strings.Cut(spec, "-")
	if !ok || !ok2 || strings.Contains(spec, ",") {
		return 0, 0, false, invalid("intervallo non supportato")
	}
	if from, err = strconv.ParseInt(a, 10, 64); err != nil || from < 0 {
		return 0, 0, false, invalid("intervallo non valido")
	}
	to = -1
	if b != "" {
		if to, err = strconv.ParseInt(b, 10, 64); err != nil || to < from {
			return 0, 0, false, invalid("intervallo non valido")
		}
	}
	return from, to, true, nil
}

// directCache returns the session's single-connection cache, making a new
// one if there is none or the last one failed (e.g. busy, then "take
// control"). A healthy cache is kept even with force: the player repeats
// the same force value on every range of the same file.
func (s *Service) directCache(sess *vodSession, force string) (*rangeCache, error) {
	s.vod.mu.Lock()
	defer s.vod.mu.Unlock()
	// A value already honoured is a retry or a later range of the same
	// address: after someone else took control it must not take it back.
	honour := force != "" && force != sess.forced
	if honour {
		sess.forced = force
	}
	if sess.cache != nil {
		sess.cache.mu.Lock()
		failed := sess.cache.err != nil
		sess.cache.mu.Unlock()
		if !failed {
			return sess.cache, nil
		}
		sess.cache.Close()
		sess.cache = nil
	}
	root := s.vod.cacheDir
	if root == "" {
		root = os.TempDir()
	}
	dir := filepath.Join(root, "direct-"+sess.id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	url, subID, viewer := sess.url, sess.subID, sess.viewer
	// Force takes the connection once. Later acquires (after an idle
	// pause, or after someone else took control) ask like anyone else,
	// so the film cannot silently take it back.
	first := honour
	sess.cache = newRangeCache(dir,
		func() (context.Context, func(), error) {
			ctx, release, err := s.acquire(context.Background(), subID, viewer, first, false)
			if err == nil {
				first = false
			}
			return ctx, release, err
		},
		func(ctx context.Context, from int64) (io.ReadCloser, int64, error) {
			resp, err := s.upstream(ctx, subID, url, fmt.Sprintf("bytes=%d-", from))
			if err != nil {
				return nil, 0, err
			}
			total := int64(-1)
			if cr := resp.Header.Get("Content-Range"); resp.StatusCode == http.StatusPartialContent && cr != "" {
				if i := strings.LastIndex(cr, "/"); i >= 0 {
					total, _ = strconv.ParseInt(cr[i+1:], 10, 64)
				}
			} else if resp.StatusCode == http.StatusOK && from == 0 {
				total = resp.ContentLength
			} else {
				resp.Body.Close()
				return nil, 0, errors.New("il provider non permette di leggere il file a pezzi")
			}
			if total <= 0 {
				resp.Body.Close()
				return nil, 0, errors.New("il provider non indica la dimensione del file")
			}
			return resp.Body, total, nil
		})
	return sess.cache, nil
}

// SetCacheDir sets where direct playback keeps its chunks (cleared now).
func (s *Service) SetCacheDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	s.vod.mu.Lock()
	s.vod.cacheDir = dir
	s.vod.mu.Unlock()
	return nil
}

const directProbeLimit = 4 * time.Second

// vodFormats are the containers ffmpeg and ffprobe may read a film as.
// Without a list they would also accept a playlist (HLS, DASH) and fetch
// its segments themselves, on the normal network, outside the
// subscription's VPN.
const vodFormats = "mov,matroska,mpegts,avi,flv,mpeg,asf,ogg"

type probeResult struct {
	Duration   float64
	VideoCodec string
	Audio      []Track
	Subtitles  []Track
}

// textSubtitles can be converted to WebVTT; image subtitles (PGS, DVD,
// DVB) cannot be shown by browsers.
var textSubtitles = map[string]bool{"subrip": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true, "text": true}

// parseProbe reads ffprobe's JSON.
func parseProbe(out []byte) (*probeResult, error) {
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type        string            `json:"codec_type"`
			Codec       string            `json:"codec_name"`
			Channels    int               `json:"channels"`
			Tags        map[string]string `json:"tags"`
			Disposition map[string]int    `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	p := &probeResult{Audio: []Track{}, Subtitles: []Track{}}
	p.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for _, st := range raw.Streams {
		t := Track{Codec: st.Codec, Channels: st.Channels, Language: strings.ToLower(st.Tags["language"]), Title: st.Tags["title"],
			Default: st.Disposition["default"] == 1, Forced: st.Disposition["forced"] == 1}
		if t.Language == "und" {
			t.Language = ""
		}
		switch st.Type {
		case "video":
			if p.VideoCodec == "" && st.Disposition["attached_pic"] == 0 {
				p.VideoCodec = st.Codec
			}
		case "audio":
			t.Index, t.Supported = len(p.Audio), true
			p.Audio = append(p.Audio, t)
		case "subtitle":
			t.Index, t.Supported = len(p.Subtitles), textSubtitles[st.Codec]
			p.Subtitles = append(p.Subtitles, t)
		}
	}
	return p, nil
}

// preferredAudio: Italian, else the file's default, else the first.
func (p *probeResult) preferredAudio() int {
	for _, t := range p.Audio {
		if t.Language == "ita" || t.Language == "it" {
			return t.Index
		}
	}
	for _, t := range p.Audio {
		if t.Default {
			return t.Index
		}
	}
	return 0
}

// probe reads duration and codecs with ffprobe through the loopback relay.
func (s *Service) probe(ctx context.Context, sess *vodSession, force bool) (*probeResult, error) {
	addr, err := s.loopback()
	if err != nil {
		return nil, err
	}
	streamCtx, release, err := s.acquire(ctx, sess.subID, sess.viewer, force, false)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(streamCtx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.ffprobeBin(), "-v", "error", "-print_format", "json", "-show_format", "-show_streams",
		"-format_whitelist", vodFormats, "http://"+addr+"/internal/"+sess.internal)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Taken over by another device during the analysis: the connection
		// is busy now, the file is not broken.
		if busy := context.Cause(streamCtx); errors.As(busy, new(ConnectionBusy)) {
			return nil, busy
		}
		return nil, fmt.Errorf("analisi del file non riuscita: %s", firstLine(stderr.String(), err))
	}
	return parseProbe(out)
}

func firstLine(s string, err error) string {
	if line, _, _ := strings.Cut(strings.TrimSpace(s), "\n"); line != "" {
		return line
	}
	return err.Error()
}

// OpenRemux starts ffmpeg at position (seconds) and returns fragmented
// MP4, or MPEG-TS when ts is set (for mpegts.js in the browser). H.264
// video and AAC audio are copied; other audio is converted to AAC stereo,
// other video to H.264 (slow on small machines).
// audio and sub choose tracks by index (-1: first audio, no subtitles).
func (s *Service) OpenRemux(ctx context.Context, sessionID string, position float64, force bool, audio, sub int, ts bool) (*Stream, error) {
	sess, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	if sess.mode != "remux" || sess.probe == nil {
		return nil, invalid("sessione non convertibile")
	}
	addr, err := s.loopback()
	if err != nil {
		return nil, err
	}
	streamCtx, release, err := s.acquire(ctx, sess.subID, sess.viewer, force, false)
	if err != nil {
		return nil, err
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	// Catch-up: the archive is asked from the minute of the position and
	// cannot be seeked, so the remaining seconds are skipped by reading.
	skip := 0.0
	if c := sess.catchup; c != nil {
		minute := time.Duration(position*float64(time.Second)) / time.Minute * time.Minute
		from := c.start.Add(minute)
		// A programme on air: the archive grows, so ask up to now (not up
		// to when playback began).
		end := c.stop
		if now := time.Now(); end.After(now) {
			end = now
		}
		s.vod.mu.Lock()
		sess.url = c.client.CatchupURL(c.streamID, from, end.Sub(from)+time.Minute, c.loc)
		s.vod.mu.Unlock()
		skip, position = position-minute.Seconds(), 0
		args = append(args, "-fflags", "+genpts+discardcorrupt", "-f", "mpegts")
	} else {
		args = append(args, "-format_whitelist", vodFormats)
	}
	if position > 0 {
		args = append(args, "-ss", strconv.FormatFloat(position, 'f', 1, 64))
	}
	p := sess.probe
	audioCodec := ""
	if audio < 0 || audio >= len(p.Audio) {
		audio = 0
	}
	if len(p.Audio) > 0 {
		audioCodec = p.Audio[audio].Codec
	}
	if ts {
		// mpegts.js stops loading for good once the browser's buffer is
		// full, and this stream cannot be asked again by range: it is sent
		// at playing speed, after the seconds skipped plus a margin.
		args = append(args, "-readrate", "1", "-readrate_initial_burst", strconv.Itoa(int(math.Ceil(skip))+30))
	}
	input := append(args, "-i", "http://"+addr+"/internal/"+sess.internal)
	if skip > 0 {
		input = append(input, "-ss", strconv.FormatFloat(skip, 'f', 1, 64))
	}
	var out []string
	out = append(out, "-map", "0:v:0", "-map", "0:a:"+strconv.Itoa(audio)+"?", "-sn", "-dn")
	if audioCodec == "aac" {
		out = append(out, "-c:a", "copy")
	} else {
		out = append(out, "-c:a", "aac", "-ac", "2", "-b:a", "192k")
	}
	contentType := "video/mp4"
	if ts {
		out, contentType = append(out, "-f", "mpegts", "pipe:1"), "video/mp2t"
	} else {
		out = append(out, "-f", "mp4", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "pipe:1")
	}
	// Text subtitles go to a second output, read by the player as it grows.
	subFile := ""
	if sub >= 0 && sub < len(p.Subtitles) && p.Subtitles[sub].Supported {
		var err error
		if subFile, err = s.subtitleFile(sess, sub, position); err != nil {
			release()
			return nil, err
		}
		out = append(out, "-map", "0:s:"+strconv.Itoa(sub), "-c:s", "webvtt", "-flush_packets", "1", "-f", "webvtt", subFile)
	}
	s.vod.mu.Lock()
	sess.subFile = subFile
	s.vod.mu.Unlock()
	// H.264 is copied; other video is converted, by the graphics card if
	// chosen, else (or when the card does not start) by the processor.
	var t TranscodeSettings
	if p.VideoCodec != "h264" {
		t, _ = s.TranscodeSettings(ctx)
	}
	body, err := s.remux(streamCtx, input, out, t, p.VideoCodec == "h264", subFile)
	if err != nil {
		release()
		return nil, err
	}
	return &Stream{ReadCloser: &releasingBody{ReadCloser: body, release: release}, ContentType: contentType,
		Title: sess.title}, nil
}

// remux starts ffmpeg; if the graphics card does not start, it converts
// with the processor.
func (s *Service) remux(ctx context.Context, input, out []string, t TranscodeSettings, copyVideo bool, subFile string) (io.ReadCloser, error) {
	body, err := s.startRemux(ctx, input, out, t, copyVideo)
	if err != nil && t.Accel != "" && !copyVideo && ctx.Err() == nil {
		slog.Warn("conversione con la scheda grafica non riuscita: uso il processore", "accel", t.Accel, "err", err)
		if subFile != "" {
			os.Remove(subFile)
		}
		body, err = s.startRemux(ctx, input, out, TranscodeSettings{}, false)
	}
	return body, err
}

// startRemux runs ffmpeg and waits for the first bytes, so errors are
// reported instead of an empty video.
func (s *Service) startRemux(ctx context.Context, input, out []string, t TranscodeSettings, copyVideo bool) (io.ReadCloser, error) {
	before, video := videoArgs(t)
	if copyVideo {
		before, video = nil, []string{"-c:v", "copy"}
	}
	// Device options are global: they can go first.
	args := append(append(append(before, input...), video...), out...)
	cmd := exec.CommandContext(ctx, s.ffmpegBin(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg non avviabile: %w", err)
	}
	body := &cmdBody{ReadCloser: stdout, cmd: cmd}
	br := bufio.NewReaderSize(body, 64<<10)
	if _, err := br.Peek(8); err != nil {
		body.Close()
		// Replaced by a newer request, taken over by another device or
		// abandoned: not a failed conversion.
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, fmt.Errorf("conversione non riuscita: %s", firstLine(stderr.String(), err))
	}
	return readCloser{br, body}, nil
}

type cmdBody struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (b *cmdBody) Close() error {
	b.ReadCloser.Close()
	if b.cmd.Process != nil {
		b.cmd.Process.Kill()
	}
	b.cmd.Wait()
	return nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

func (s *Service) ffmpegBin() string {
	if s.vod.ffmpeg != "" {
		return s.vod.ffmpeg
	}
	return "ffmpeg"
}

// pacedRemux says whether ffmpeg has -readrate_initial_burst (FFmpeg 6.1
// and later; Debian 12 has 5.1), which the MPEG-TS remux needs. ffmpeg
// does not change while the server runs, so the answer is kept until
// SetFFmpeg; a probe that did not finish (timeout, no ffmpeg) is not, or
// one slow moment would send the browser to MP4 for good.
func (s *Service) pacedRemux() bool {
	bin := s.ffmpegBin()
	s.vod.mu.Lock()
	known, paced := s.vod.pacedBin == bin, s.vod.paced
	s.vod.mu.Unlock()
	if known {
		return paced
	}
	// Not the request's context: a viewer who leaves must not decide.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-hide_banner", "-h", "long").Output()
	paced = bytes.Contains(out, []byte("-readrate_initial_burst"))
	if err == nil {
		s.vod.mu.Lock()
		s.vod.pacedBin, s.vod.paced = bin, paced
		s.vod.mu.Unlock()
	}
	return paced
}

func (s *Service) ffprobeBin() string {
	if s.vod.ffprobe != "" {
		return s.vod.ffprobe
	}
	return "ffprobe"
}

// loopback starts (once) the relay used by ffmpeg and ffprobe. It listens
// on 127.0.0.1 only and serves only session tokens it issued; requests are
// forwarded to the provider through the VPN with their Range header. The
// provider connection is the one already reserved by the session.
func (s *Service) loopback() (string, error) {
	var err error
	s.vod.once.Do(func() {
		var l net.Listener
		l, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return
		}
		s.vod.loopback = l.Addr().String()
		srv := &http.Server{Handler: http.HandlerFunc(s.serveLoopback), ReadHeaderTimeout: 10 * time.Second}
		go srv.Serve(l)
	})
	if s.vod.loopback == "" {
		if err == nil {
			err = errors.New("relay locale non disponibile")
		}
		return "", err
	}
	return s.vod.loopback, nil
}

func (s *Service) serveLoopback(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/internal/")
	s.vod.mu.Lock()
	sess := s.vod.byToken[token]
	var target string
	if sess != nil {
		target = sess.url
	}
	s.vod.mu.Unlock()
	if sess == nil || r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	resp, err := s.upstream(r.Context(), sess.subID, target, r.Header.Get("Range"))
	if err != nil {
		http.Error(w, "upstream", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (s *Service) subtitleFile(sess *vodSession, sub int, position float64) (string, error) {
	s.vod.mu.Lock()
	defer s.vod.mu.Unlock()
	if sess.subDir == "" {
		dir, err := os.MkdirTemp("", "vodmax-subs-")
		if err != nil {
			return "", err
		}
		sess.subDir = dir
	} else {
		// One file at a time per session.
		old, _ := filepath.Glob(filepath.Join(sess.subDir, "*.vtt"))
		for _, f := range old {
			os.Remove(f)
		}
	}
	return filepath.Join(sess.subDir, fmt.Sprintf("%d-%d.vtt", sub, int(position))), nil
}

// Subtitles returns the WebVTT written so far by the session's remux (cue
// times start from the remux position).
func (s *Service) Subtitles(sessionID string) ([]byte, error) {
	sess, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	s.vod.mu.Lock()
	file := sess.subFile
	s.vod.mu.Unlock()
	if file == "" {
		return []byte("WEBVTT\n"), nil
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("WEBVTT\n"), nil
	}
	return data, err
}
