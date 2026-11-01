package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Live TV "like Sky": the channel being watched is written by ffmpeg as
// HLS segments into a buffer on disk (video copied, audio converted to
// AAC for browsers), so it can be paused, rewound and brought back to
// live. One buffer per viewer: tuning another channel replaces it. The
// provider stream is the one OpenLive gives (same connection rules, a
// channel being recorded is read from the recording).

const (
	segmentSeconds    = 2
	defaultWindowMins = 120
	maxWindowMins     = 360
	timeshiftIdle     = 90 * time.Second // no request from the player: stop
	firstSegmentWait  = 25 * time.Second
	minFreeTimeshift  = 2 << 30
	liveModeKey       = "live"
)

var timeshiftFile = regexp.MustCompile(`^(index\.m3u8|seg[0-9]{6}\.ts)$`)

// LiveSettings choose how live TV plays.
type LiveSettings struct {
	Mode          string `json:"mode"`           // "timeshift" (pause and rewind) or "direct" (lowest delay)
	WindowMinutes int    `json:"window_minutes"` // how far back, timeshift only
}

func (s *Service) LiveSettings(ctx context.Context) (LiveSettings, error) {
	ls := LiveSettings{Mode: "timeshift", WindowMinutes: defaultWindowMins}
	err := s.st.Setting(ctx, liveModeKey, &ls)
	return ls, err
}

func (s *Service) SetLiveSettings(ctx context.Context, ls LiveSettings) error {
	if ls.Mode != "timeshift" && ls.Mode != "direct" {
		return invalid("modalità non valida: timeshift o direct")
	}
	if ls.WindowMinutes < 10 || ls.WindowMinutes > maxWindowMins {
		return invalid("la finestra va da 10 a %d minuti", maxWindowMins)
	}
	return s.st.SetSetting(ctx, liveModeKey, ls)
}

type tsSession struct {
	id, viewer string
	itemID     int64
	dir        string
	cancel     context.CancelFunc
	done       chan struct{}

	mu       sync.Mutex
	lastSeen time.Time
	err      string
}

func (t *tsSession) touch() {
	t.mu.Lock()
	t.lastSeen = time.Now()
	t.mu.Unlock()
}

type timeshiftState struct {
	mu       sync.Mutex
	dir      string
	log      *slog.Logger
	sessions map[string]*tsSession
}

// SetTimeshiftDir sets where buffers go; leftovers of a previous run are
// removed.
func (s *Service) SetTimeshiftDir(dir string, log *slog.Logger) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	s.ts.mu.Lock()
	s.ts.dir, s.ts.log, s.ts.sessions = dir, log, map[string]*tsSession{}
	s.ts.mu.Unlock()
	return nil
}

// TimeshiftSession is what the player needs.
type TimeshiftSession struct {
	ID            string `json:"id"`
	URL           string `json:"url"`            // HLS playlist
	KeepAlive     string `json:"keepalive"`      // POST while the page is open
	Stop          string `json:"stop"`           // POST when leaving
	WindowSeconds int    `json:"window_seconds"` // how far back the buffer goes, at most
}

// ErrNoTimeshift: buffers are not available (not configured, disk full);
// the client falls back to the direct stream.
var ErrNoTimeshift = errors.New("diretta con pausa non disponibile")

// StartTimeshift opens a channel for viewer into a new buffer and returns
// when the first segment is ready.
func (s *Service) StartTimeshift(ctx context.Context, itemID int64, viewer string, force bool) (TimeshiftSession, error) {
	s.ts.mu.Lock()
	root, log := s.ts.dir, s.ts.log
	s.ts.mu.Unlock()
	if root == "" {
		return TimeshiftSession{}, ErrNoTimeshift
	}
	if free, _, ok := diskSpace(root); ok && free < minFreeTimeshift {
		return TimeshiftSession{}, fmt.Errorf("%w: disco quasi pieno", ErrNoTimeshift)
	}
	// Before taking a connection (maybe from another viewer): without
	// ffmpeg there is no buffer, and the player uses the direct stream.
	if _, err := exec.LookPath(s.ffmpegBin()); err != nil {
		return TimeshiftSession{}, fmt.Errorf("%w: ffmpeg non avviabile: %v", ErrNoTimeshift, err)
	}
	ls, err := s.LiveSettings(ctx)
	if err != nil {
		return TimeshiftSession{}, err
	}
	// The viewer's previous buffer goes first (and with it its connection).
	s.stopViewerTimeshift(viewer)

	ticket, err := s.LiveTicket(ctx, itemID, viewer)
	if err != nil {
		return TimeshiftSession{}, err
	}
	sess := &tsSession{id: randomToken(), viewer: viewer, itemID: itemID, done: make(chan struct{}), lastSeen: time.Now()}
	sess.dir = filepath.Join(root, sess.id)
	if err := os.Mkdir(sess.dir, 0o700); err != nil {
		return TimeshiftSession{}, err
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	sess.cancel = cancel
	first, err := s.OpenLive(runCtx, ticket, force)
	if err != nil {
		cancel()
		os.RemoveAll(sess.dir)
		return TimeshiftSession{}, err
	}
	segments := ls.WindowMinutes * 60 / segmentSeconds
	cmd := exec.CommandContext(runCtx, s.ffmpegBin(), "-hide_banner", "-loglevel", "error",
		"-fflags", "+genpts+discardcorrupt", "-f", "mpegts", "-i", "pipe:0",
		"-map", "0:v:0?", "-map", "0:a:0?", "-c:v", "copy", "-c:a", "aac", "-ac", "2", "-b:a", "160k",
		"-f", "hls", "-hls_time", strconv.Itoa(segmentSeconds), "-hls_list_size", strconv.Itoa(segments),
		"-hls_flags", "delete_segments+program_date_time+independent_segments+temp_file",
		"-hls_segment_filename", filepath.Join(sess.dir, "seg%06d.ts"), filepath.Join(sess.dir, "index.m3u8"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		first.Close()
		cancel()
		os.RemoveAll(sess.dir)
		return TimeshiftSession{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		first.Close()
		cancel()
		os.RemoveAll(sess.dir)
		// Without ffmpeg the channel still plays, without pause: the
		// player falls back to the direct stream (ErrNoTimeshift).
		return TimeshiftSession{}, fmt.Errorf("%w: ffmpeg non avviabile: %v", ErrNoTimeshift, err)
	}
	s.ts.mu.Lock()
	s.ts.sessions[sess.id] = sess
	s.ts.mu.Unlock()

	// Feed ffmpeg; when the provider stream drops, open it again.
	go func() {
		stream := io.ReadCloser(first)
		for attempt := 0; runCtx.Err() == nil; attempt++ {
			_, err := io.Copy(stdin, stream)
			stream.Close()
			if runCtx.Err() != nil || attempt >= 5 {
				if runCtx.Err() == nil && log != nil {
					log.Warn("diretta con pausa: flusso perso", "err", err)
				}
				break
			}
			select {
			case <-runCtx.Done():
			case <-time.After(2 * time.Second):
			}
			t, err := s.LiveTicket(runCtx, itemID, viewer)
			if err != nil {
				break
			}
			next, err := s.OpenLive(runCtx, t, false)
			if err != nil {
				sess.mu.Lock()
				sess.err = err.Error()
				sess.mu.Unlock()
				break
			}
			stream = next
		}
		stdin.Close()
	}()
	// ffmpeg ends with the input; then the buffer is removed.
	go func() {
		cmd.Wait()
		cancel()
		s.ts.mu.Lock()
		if s.ts.sessions[sess.id] == sess {
			delete(s.ts.sessions, sess.id)
		}
		s.ts.mu.Unlock()
		os.RemoveAll(sess.dir)
		close(sess.done)
	}()
	// Stop when the player is gone.
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-sess.done:
				return
			case <-t.C:
				sess.mu.Lock()
				idle := time.Since(sess.lastSeen) > timeshiftIdle
				sess.mu.Unlock()
				if idle {
					cancel()
					return
				}
			}
		}
	}()

	if err := waitFirstSegment(ctx, sess); err != nil {
		cancel()
		<-sess.done
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return TimeshiftSession{}, fmt.Errorf("diretta con pausa non avviata: %s", firstLine(msg, err))
		}
		return TimeshiftSession{}, err
	}
	base := "/stream/timeshift/" + sess.id
	return TimeshiftSession{ID: sess.id, URL: base + "/index.m3u8", KeepAlive: base + "/keepalive", Stop: base + "/stop",
		WindowSeconds: ls.WindowMinutes * 60}, nil
}

func waitFirstSegment(ctx context.Context, sess *tsSession) error {
	deadline := time.NewTimer(firstSegmentWait)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if f, err := os.Open(filepath.Join(sess.dir, "index.m3u8")); err == nil {
			ok := false
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if strings.HasPrefix(sc.Text(), "seg") {
					ok = true
				}
			}
			f.Close()
			if ok {
				return nil
			}
		}
		select {
		case <-sess.done:
			sess.mu.Lock()
			defer sess.mu.Unlock()
			if sess.err != "" {
				return errors.New(sess.err)
			}
			return errors.New("il canale non trasmette")
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("il canale non ha mandato video in tempo")
		case <-tick.C:
		}
	}
}

// StopViewerTimeshift ends a viewer's buffer: the viewer is about to use
// its connection for something else (a film, the direct stream), and the
// buffer must not take it back.
func (s *Service) StopViewerTimeshift(viewer string) { s.stopViewerTimeshift(viewer) }

func (s *Service) stopViewerTimeshift(viewer string) {
	s.ts.mu.Lock()
	var old []*tsSession
	for _, t := range s.ts.sessions {
		if t.viewer == viewer {
			old = append(old, t)
		}
	}
	s.ts.mu.Unlock()
	for _, t := range old {
		t.cancel()
		<-t.done
	}
}

// ErrTimeshift: unknown or ended buffer.
var ErrTimeshift = errors.New("diretta con pausa terminata: riapri il canale")

func (s *Service) timeshiftSession(id string) (*tsSession, error) {
	s.ts.mu.Lock()
	defer s.ts.mu.Unlock()
	t := s.ts.sessions[id]
	if t == nil {
		return nil, ErrTimeshift
	}
	return t, nil
}

// TimeshiftFile returns the path of a playlist or segment of a buffer.
func (s *Service) TimeshiftFile(id, name string) (string, error) {
	if !timeshiftFile.MatchString(name) {
		return "", ErrTimeshift
	}
	t, err := s.timeshiftSession(id)
	if err != nil {
		return "", err
	}
	t.touch()
	return filepath.Join(t.dir, name), nil
}

func (s *Service) KeepTimeshift(id string) error {
	t, err := s.timeshiftSession(id)
	if err != nil {
		return err
	}
	t.touch()
	return nil
}

func (s *Service) StopTimeshift(id string) {
	if t, err := s.timeshiftSession(id); err == nil {
		t.cancel()
		<-t.done
	}
}
