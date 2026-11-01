package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/silirim/vodmax/internal/store"
)

// Recordings are made by the server: at start time it opens the channel
// through the VPN (taking priority over viewers) and writes the MPEG-TS
// stream to disk until the stop time, reconnecting if the stream drops.

const (
	recordBefore  = 2 * time.Minute
	recordAfter   = 5 * time.Minute
	maxRecording  = 6 * time.Hour
	schedulerTick = 5 * time.Second
)

type activeRec struct {
	rec    store.Recording
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	bytes  int64
}

type recorderState struct {
	mu      sync.Mutex
	dir     string
	running map[int64]*activeRec
	log     *slog.Logger
	tickets map[string]recTicket
	comskip string
	stall   time.Duration // no data for this long: reconnect (0: recordStall)
}

// recordStall is how long a recording waits for data before it reconnects:
// a provider can keep the connection open and send nothing.
const recordStall = 20 * time.Second

type recTicket struct {
	id      int64
	expires time.Time
}

// StartRecorder runs the scheduler until ctx ends. Recordings left
// "recording" by a previous run (crash, restart) are marked failed with
// what they managed to save.
// SetRecordStall changes how long a recording waits for data (tests).
func (s *Service) SetRecordStall(d time.Duration) { s.rec.stall = d }

func (s *Service) StartRecorder(ctx context.Context, dir string, log *slog.Logger) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	s.rec.mu.Lock()
	s.rec.dir, s.rec.log, s.rec.running = dir, log, map[int64]*activeRec{}
	s.rec.mu.Unlock()
	interrupted, err := s.st.RecordingsInState(ctx, "recording")
	if err != nil {
		return err
	}
	for _, r := range interrupted {
		r.Status, r.Error = "failed", "interrotta da un riavvio del server"
		if fi, err := os.Stat(r.File); err == nil && fi.Size() > 0 {
			r.Bytes = fi.Size()
		}
		s.st.UpdateRecording(ctx, r)
	}
	go func() {
		t := time.NewTicker(schedulerTick)
		defer t.Stop()
		for {
			s.scheduleDue(ctx)
			select {
			case <-ctx.Done():
				s.rec.mu.Lock()
				for _, a := range s.rec.running {
					a.cancel()
				}
				s.rec.mu.Unlock()
				return
			case <-t.C:
			}
		}
	}()
	return nil
}

func (s *Service) scheduleDue(ctx context.Context) {
	s.rec.mu.Lock()
	started := s.rec.running != nil
	s.rec.mu.Unlock()
	if !started {
		return // recorder not running (tests, tools)
	}
	due, err := s.st.DueRecordings(ctx, time.Now())
	if err != nil {
		return
	}
	for _, r := range due {
		if time.Now().Unix() >= r.Stop {
			r.Status, r.Error = "failed", "non avviata in tempo"
			s.st.UpdateRecording(ctx, r)
			continue
		}
		s.rec.mu.Lock()
		_, already := s.rec.running[r.ID]
		s.rec.mu.Unlock()
		if !already {
			s.startRecording(ctx, r)
		}
	}
}

func (s *Service) startRecording(parent context.Context, r store.Recording) {
	if free, _, ok := diskSpace(s.rec.dir); ok && free < minFreeToRecord {
		r.Status, r.Error = "failed", fmt.Sprintf("spazio su disco esaurito (%.1f GB liberi)", float64(free)/(1<<30))
		s.st.UpdateRecording(parent, r)
		return
	}
	ctx, cancel := context.WithDeadline(parent, time.Unix(r.Stop, 0))
	a := &activeRec{rec: r, cancel: cancel, done: make(chan struct{})}
	r.Status = "recording"
	r.File = filepath.Join(s.rec.dir, fmt.Sprintf("%d-%s.ts", r.ID, time.Unix(r.Start, 0).Format("20060102-1504")))
	a.rec = r
	s.st.UpdateRecording(parent, r)
	s.rec.mu.Lock()
	s.rec.running[r.ID] = a
	s.rec.mu.Unlock()
	go func() {
		defer close(a.done)
		defer cancel()
		err := s.record(ctx, a)
		s.rec.mu.Lock()
		delete(s.rec.running, r.ID)
		s.rec.mu.Unlock()
		a.mu.Lock()
		final := a.rec
		final.Bytes = a.bytes
		a.mu.Unlock()
		switch {
		case final.Status == "cancelled":
		case final.Bytes == 0:
			final.Status = "failed"
			if err != nil {
				final.Error = err.Error()
			} else {
				final.Error = "nessun dato ricevuto"
			}
		default:
			final.Status = "completed"
			if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				final.Error = "registrazione parziale: " + err.Error()
			}
		}
		if final.Status == "completed" {
			if mp4, err := s.remuxRecording(final.File); err == nil {
				final.File = mp4
				if fi, err := os.Stat(mp4); err == nil {
					final.Bytes = fi.Size()
				}
			} else if s.rec.log != nil {
				s.rec.log.Warn("registrazione lasciata in MPEG-TS", "id", r.ID, "err", err)
			}
		}
		s.st.UpdateRecording(context.Background(), final)
		if s.rec.log != nil {
			s.rec.log.Info("registrazione terminata", "id", r.ID, "stato", final.Status, "byte", final.Bytes)
		}
		if final.Status == "completed" {
			s.findCommercials(final.File)
		}
	}()
}

func (a *activeRec) size() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bytes
}

// record writes the channel to the file until ctx ends, reconnecting on
// errors. It returns the last error, if any.
func (s *Service) record(ctx context.Context, a *activeRec) error {
	f, err := os.OpenFile(a.rec.File, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	var lastErr error
	for ctx.Err() == nil {
		it, err := s.st.ChannelForGroup(ctx, a.rec.GroupKey)
		if err != nil {
			return fmt.Errorf("canale non più disponibile: %w", err)
		}
		lastErr = s.recordOnce(ctx, a, it, f)
		if ctx.Err() != nil {
			// Reaching the stop time closes the stream: not an error.
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				lastErr = nil
			}
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second): // reconnect
		}
	}
	return lastErr
}

func (s *Service) recordOnce(ctx context.Context, a *activeRec, it store.PlaybackItem, f *os.File) error {
	streamCtx, release, err := s.acquire(ctx, it.SubscriptionID, "recording:"+strconv.FormatInt(a.rec.ID, 10), true, true)
	if err != nil {
		return err
	}
	defer release()
	link, err := s.liveURL(streamCtx, it)
	if err != nil {
		return err
	}
	readCtx, cancel := context.WithCancel(streamCtx)
	defer cancel()
	resp, err := s.upstream(readCtx, it.SubscriptionID, link, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	stall := s.rec.stall
	if stall <= 0 {
		stall = recordStall
	}
	var last atomic.Int64
	var stalled atomic.Bool
	last.Store(time.Now().UnixNano())
	go func() {
		t := time.NewTicker(stall / 10)
		defer t.Stop()
		for {
			select {
			case <-readCtx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, last.Load())) > stall {
					stalled.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	buf := make([]byte, 188*512)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			last.Store(time.Now().UnixNano())
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			a.mu.Lock()
			a.bytes += int64(n)
			a.mu.Unlock()
		}
		if err != nil {
			if stalled.Load() && ctx.Err() == nil {
				return errors.New("il provider ha smesso di mandare dati")
			}
			if err == io.EOF {
				return errors.New("flusso terminato dal provider")
			}
			return err
		}
	}
}

// ---- Scheduling ----

// RecordingRequest schedules a recording. Without Start/Stop, the
// programme on air on the channel is recorded, with margins; explicit
// times are used as given.
type RecordingRequest struct {
	ChannelID int64  `json:"channel_id"`
	Start     int64  `json:"start,omitempty"`
	Stop      int64  `json:"stop,omitempty"`
	Title     string `json:"title,omitempty"`
	// FromGuide: start and stop are a guide programme's, so the margins
	// apply as for the programme on air.
	FromGuide bool `json:"from_guide,omitempty"`
}

func (s *Service) ScheduleRecording(ctx context.Context, req RecordingRequest) (store.Recording, error) {
	s.rec.mu.Lock()
	recorder := s.rec.dir != ""
	s.rec.mu.Unlock()
	if !recorder {
		return store.Recording{}, invalid("su questo dispositivo non si registra: le registrazioni sono del server di casa")
	}
	it, err := s.st.PlaybackItem(ctx, req.ChannelID)
	if err != nil {
		return store.Recording{}, err
	}
	if it.Kind != "live" {
		return store.Recording{}, invalid("si possono registrare solo i canali in diretta")
	}
	group, err := s.st.GroupKeyOf(ctx, it.ID)
	if err != nil {
		return store.Recording{}, err
	}
	now := time.Now()
	start, stop, title := req.Start, req.Stop, req.Title
	margins := true
	if start == 0 && stop == 0 {
		guide, err := s.st.ChannelGuide(ctx, it.ID, now, now.Add(time.Second))
		if err != nil {
			return store.Recording{}, err
		}
		for _, p := range guide {
			if p.Start <= now.Unix() && p.Stop > now.Unix() {
				start, stop = p.Start, p.Stop
				if title == "" {
					title = p.Title
				}
				break
			}
		}
		if stop == 0 {
			return store.Recording{}, invalid("nessun programma in guida per questo canale: indica inizio e fine")
		}
	} else {
		margins = req.FromGuide
	}
	if margins {
		// Guide times are approximate: record a little before and after.
		start = time.Unix(start, 0).Add(-recordBefore).Unix()
		stop = time.Unix(stop, 0).Add(recordAfter).Unix()
	}
	switch {
	case stop <= start:
		return store.Recording{}, invalid("la fine deve essere dopo l'inizio")
	case stop <= now.Unix():
		return store.Recording{}, invalid("il programma è già finito")
	case time.Duration(stop-start)*time.Second > maxRecording:
		return store.Recording{}, invalid("durata massima di una registrazione: 6 ore")
	}
	if title == "" {
		title = it.Title
	}
	if other, err := s.st.OverlappingRecording(ctx, group, start, stop); err != nil {
		return store.Recording{}, err
	} else if other != nil {
		return store.Recording{}, invalid("c'è già una registrazione di questo canale in quell'orario: %s", other.Title)
	}
	if why, err := s.recordingConflict(ctx, it.SubscriptionID, group, start, stop); err != nil {
		return store.Recording{}, err
	} else if why != "" {
		return store.Recording{}, invalid("non registrabile: %s", why)
	}
	rec := store.Recording{ItemID: it.ID, GroupKey: group, Channel: it.Title, Title: title, Start: start, Stop: stop}
	id, err := s.st.AddRecording(ctx, rec)
	if err != nil {
		return store.Recording{}, err
	}
	rec, err = s.st.Recording(ctx, id)
	if err == nil && start <= now.Unix() {
		s.scheduleDue(context.WithoutCancel(ctx)) // starts now, not at the next tick
	}
	return rec, err
}

// Recordings lists recordings with the live size of running ones.
func (s *Service) Recordings(ctx context.Context) ([]store.Recording, error) {
	list, err := s.st.Recordings(ctx)
	if viewerOf(ctx).Kids {
		kept := list[:0]
		for _, r := range list {
			if s.allowGroup(ctx, r.GroupKey) == nil {
				kept = append(kept, r)
			}
		}
		list = kept
	}
	s.rec.mu.Lock()
	for i := range list {
		if a := s.rec.running[list[i].ID]; a != nil {
			list[i].Bytes = a.size()
			list[i].Status = "recording"
		}
	}
	s.rec.mu.Unlock()
	return list, err
}

// DeleteRecording cancels a scheduled or running recording, or deletes a
// finished one and its file.
func (s *Service) DeleteRecording(ctx context.Context, id int64) error {
	r, err := s.st.Recording(ctx, id)
	if err != nil {
		return err
	}
	s.rec.mu.Lock()
	a := s.rec.running[id]
	s.rec.mu.Unlock()
	if a != nil {
		a.mu.Lock()
		a.rec.Status = "cancelled"
		a.mu.Unlock()
		a.cancel()
		<-a.done
	}
	if r.File != "" {
		if err := os.Remove(r.File); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		os.Remove(edlPath(r.File))
	}
	if r.RuleID != 0 && r.Stop > time.Now().Unix() {
		// Its rule must not schedule it again.
		return s.st.CancelRecording(ctx, id)
	}
	return s.st.DeleteRecording(ctx, id)
}

// ---- Watching recordings ----

// activeRecordingFor returns the running recording of the channel an item
// belongs to, if any.
func (s *Service) activeRecordingFor(ctx context.Context, itemID int64) *activeRec {
	group, err := s.st.GroupKeyOf(ctx, itemID)
	if err != nil {
		return nil
	}
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	for _, a := range s.rec.running {
		if a.rec.GroupKey == group {
			return a
		}
	}
	return nil
}

// followRecording streams a growing recording from close to its end, like
// live TV, until the recording stops.
func (s *Service) followRecording(ctx context.Context, a *activeRec) (*Stream, error) {
	f, err := os.Open(a.rec.File)
	if err != nil {
		return nil, err
	}
	const back = 188 * 10000 // ~2 MB before the live edge
	if size := a.size(); size > back {
		f.Seek((size-back)/188*188, io.SeekStart)
	}
	return &Stream{ReadCloser: &follower{f: f, ctx: ctx, a: a}, ContentType: "video/mp2t", Title: a.rec.Channel}, nil
}

type follower struct {
	f   *os.File
	ctx context.Context
	a   *activeRec
}

func (r *follower) Read(p []byte) (int, error) {
	for {
		n, err := r.f.Read(p)
		if n > 0 || (err != nil && err != io.EOF) {
			return n, err
		}
		select {
		case <-r.ctx.Done():
			return 0, io.EOF
		case <-r.a.done:
			return r.f.Read(p) // last bytes, then EOF
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (r *follower) Close() error { return r.f.Close() }

// RecordingTicket authorises playback of a recording for 8 hours (range
// requests reuse it).
// RecordingTicket authorises watching a recording; the advertising breaks
// found in it come along.
func (s *Service) RecordingTicket(ctx context.Context, id int64) (string, []Commercial, error) {
	r, err := s.st.Recording(ctx, id)
	if err != nil {
		return "", nil, err
	}
	if err := s.allowGroup(ctx, r.GroupKey); err != nil {
		return "", nil, err
	}
	var breaks []Commercial
	if r.Status == "completed" && r.File != "" {
		breaks = commercials(r.File)
	}
	t := randomToken()
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	if s.rec.tickets == nil {
		s.rec.tickets = map[string]recTicket{}
	}
	now := time.Now()
	for k, v := range s.rec.tickets {
		if now.After(v.expires) {
			delete(s.rec.tickets, k)
		}
	}
	s.rec.tickets[t] = recTicket{id: id, expires: now.Add(8 * time.Hour)}
	return t, breaks, nil
}

// OpenRecording returns the recording file (finished: seekable) or, if it
// is still being recorded, a stream that follows it.
func (s *Service) OpenRecording(ctx context.Context, ticket string) (*os.File, *Stream, store.Recording, error) {
	s.rec.mu.Lock()
	tk, ok := s.rec.tickets[ticket]
	s.rec.mu.Unlock()
	if !ok || time.Now().After(tk.expires) {
		return nil, nil, store.Recording{}, ErrTicket
	}
	r, err := s.st.Recording(ctx, tk.id)
	if err != nil {
		return nil, nil, r, err
	}
	s.rec.mu.Lock()
	a := s.rec.running[r.ID]
	s.rec.mu.Unlock()
	if a != nil {
		f, err := os.Open(r.File)
		if err != nil {
			return nil, nil, r, err
		}
		return nil, &Stream{ReadCloser: &follower{f: f, ctx: ctx, a: a}, ContentType: "video/mp2t", Title: r.Title}, r, nil
	}
	if r.File == "" {
		return nil, nil, r, ErrNotReady
	}
	f, err := os.Open(r.File)
	return f, nil, r, err
}

// ErrNotReady means the recording has no file yet.
var ErrNotReady = errNotReady

// remuxRecording copies a finished MPEG-TS recording into an MP4 with the
// index at the start, so browsers can seek in it; the TS is removed on
// success. Nothing is re-encoded.
func (s *Service) remuxRecording(ts string) (string, error) {
	mp4 := strings.TrimSuffix(ts, ".ts") + ".mp4"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.ffmpegBin(), "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", ts, "-map", "0:v:0", "-map", "0:a?", "-c", "copy", "-movflags", "+faststart", mp4)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
	if err := cmd.Run(); err != nil {
		os.Remove(mp4)
		return "", fmt.Errorf("%s", firstLine(stderr.String(), err))
	}
	if fi, err := os.Stat(mp4); err != nil || fi.Size() == 0 {
		os.Remove(mp4)
		return "", errors.New("file MP4 vuoto")
	}
	os.Remove(ts)
	return mp4, nil
}
