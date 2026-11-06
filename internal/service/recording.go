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
	"slices"
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
	// The seconds the file holds: those of the connections that ended
	// (and of the part before a restart), plus the current connection's
	// from its first to its latest data. Gaps between connections hold no
	// bytes: counting them put seeks minutes off.
	secs       float64
	from, last time.Time
	early      bool // stopped by a person before the end: what is there is kept
	// contended: the provider closed it again and again soon after it
	// opened, as when another device uses the same account.
	contended bool
}

type recorderState struct {
	mu      sync.Mutex
	dir     string
	running map[int64]*activeRec
	log     *slog.Logger
	tickets map[string]recTicket
	comskip string
	resumed []int64         // recordings carried on after a restart
	ctx     context.Context // the recorder's: it ends when the server stops
	stall   time.Duration   // no data for this long: reconnect (0: recordStall)
	// converting: finished recordings being turned into MP4.
	converting map[int64]bool
	// files serialises the switch to the MP4 with deletions, so that
	// neither leaves a file without its recording.
	files sync.Mutex
	// contention: per subscription, the last recording cut again and
	// again soon after each reconnection (shown in the status).
	contention map[int64]contention
}

type contention struct {
	title string
	at    time.Time
}

// recordStall is how long a recording waits for data before it reconnects:
// a provider can keep the connection open and send nothing.
const recordStall = 20 * time.Second

const (
	// A recording reconnects at once (3 s) after a connection that brought
	// data: insisting saved the match of 05/11 while another client of the
	// account kept taking the connection. While nothing arrives (channel
	// gone, provider refusing) the pause doubles up to a minute.
	recordRetry    = 3 * time.Second
	recordRetryMax = time.Minute
	// After a connection that brought nothing, a recording takes the
	// connection from a viewer only this often: a channel that does not
	// work would otherwise cut the family's TV every few seconds for the
	// whole programme. A channel that comes back is still recorded.
	recordPriorityEvery = 5 * time.Minute
	// shortSessions connections in a row that the provider closes within
	// shortSession of opening them: probably another device using the
	// account (the status and the recording say so).
	shortSession  = time.Minute
	shortSessions = 3
	// contentionShown is how long the status keeps saying it.
	contentionShown = 12 * time.Hour
)

// errDiskWrite ends a recording: retrying cannot help, and what was
// written is kept.
var errDiskWrite = errors.New("scrittura sul disco non riuscita: disco pieno o non scrivibile")

type recTicket struct {
	id      int64
	expires time.Time
}

// SetRecordStall changes how long a recording, or a live buffer, waits
// for data (tests).
func (s *Service) SetRecordStall(d time.Duration) { s.rec.stall = d }

// StartRecorder runs the scheduler until ctx ends. Recordings left
// "recording" by a previous run (crash, restart, update) carry on in the
// same file if their programme is still on; the others keep what they
// managed to save.
func (s *Service) StartRecorder(ctx context.Context, dir string, log *slog.Logger) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	s.rec.mu.Lock()
	s.rec.dir, s.rec.log, s.rec.running, s.rec.ctx = dir, log, map[int64]*activeRec{}, ctx
	s.rec.mu.Unlock()
	interrupted, err := s.st.RecordingsInState(ctx, "recording")
	if err != nil {
		return err
	}
	for _, r := range interrupted {
		if fi, err := os.Stat(r.File); err == nil && fi.Size() > 0 {
			r.Bytes = fi.Size()
		}
		switch {
		case time.Now().Unix() < r.Stop:
			r.Status, r.Error = "scheduled", "" // resumed by scheduleDue
			if log != nil {
				log.Info("registrazione ripresa dopo il riavvio", "id", r.ID, "byte", r.Bytes)
			}
			s.rec.resumed = append(s.rec.resumed, r.ID)
		case r.Bytes > 0:
			r.Status, r.Error = "completed", "registrazione parziale: interrotta da un riavvio del server"
		default:
			r.Status, r.Error = "failed", "interrotta da un riavvio del server"
		}
		s.st.UpdateRecording(ctx, r)
	}
	go s.convertLeftovers(ctx)
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

// WaitRecorder waits, at most d, for the recordings to save their state
// after the recorder's context ended (server stopping).
func (s *Service) WaitRecorder(d time.Duration) {
	s.rec.mu.Lock()
	var done []chan struct{}
	for _, a := range s.rec.running {
		a.cancel()
		done = append(done, a.done)
	}
	s.rec.mu.Unlock()
	until := time.After(d)
	for _, c := range done {
		select {
		case <-c:
		case <-until:
			return
		}
	}
}

func (s *Service) scheduleDue(ctx context.Context) {
	s.rec.mu.Lock()
	started := s.rec.running != nil
	parent := s.rec.ctx
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
			// Recordings belong to the recorder, not to the request that
			// scheduled them: they end when the server stops.
			s.startRecording(parent, r)
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
	s.rec.mu.Lock()
	resumed := slices.Contains(s.rec.resumed, r.ID)
	s.rec.mu.Unlock()
	if fi, err := os.Stat(r.File); err == nil && resumed {
		a.bytes = fi.Size() // the file continues (record appends)
		// Last written when the server stopped: the time it was off holds
		// nothing.
		a.secs = max(0, fi.ModTime().Sub(time.Unix(r.Start, 0)).Seconds())
	}
	a.rec = r
	s.st.UpdateRecording(parent, r)
	s.rec.mu.Lock()
	s.rec.running[r.ID] = a
	s.rec.mu.Unlock()
	go func() {
		defer cancel()
		err := s.record(ctx, a)
		a.mu.Lock()
		final := a.rec
		final.Bytes = a.bytes
		secs := a.secs
		a.mu.Unlock()
		// The server is stopping (restart, update) while the programme is
		// still on: the recording stays "recording" and the next start
		// carries on in the same file.
		if final.Status != "cancelled" && parent.Err() != nil && time.Now().Unix() < final.Stop {
			s.st.UpdateRecording(context.Background(), final)
			s.endRunning(a)
			if s.rec.log != nil {
				s.rec.log.Info("registrazione sospesa per l'arresto del server", "id", r.ID, "byte", final.Bytes)
			}
			return
		}
		if now := time.Now().Unix(); final.Status != "cancelled" && now < final.Stop {
			final.Stop = now // ended before its time: the list and keep_days use the real end
		}
		switch {
		case final.Status == "cancelled":
		case final.Bytes == 0:
			final.Status = "failed"
			os.Remove(final.File) // nothing in it: no empty file left behind
			final.File = ""
			if err != nil {
				final.Error = err.Error()
			} else {
				final.Error = "nessun dato ricevuto"
			}
		default:
			final.Status = "completed"
			a.mu.Lock()
			early := a.early
			a.mu.Unlock()
			if early {
				final.Error = "fermata prima della fine"
			} else if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				final.Error = "registrazione parziale: " + err.Error()
			} else if resumed {
				final.Error = "registrazione parziale: interrotta da un riavvio del server"
			}
			a.mu.Lock()
			contended := a.contended
			a.mu.Unlock()
			if contended {
				if final.Error == "" {
					final.Error = "registrazione parziale"
				}
				final.Error += " (forse un altro dispositivo usa l'abbonamento)"
			}
		}
		// Saved as finished before the conversion: stopping does not wait
		// for it, and a restart in the middle cannot bring back a
		// recording that was stopped.
		s.st.UpdateRecording(context.Background(), final)
		s.endRunning(a)
		if s.rec.log != nil {
			s.rec.log.Info("registrazione terminata", "id", r.ID, "stato", final.Status, "byte", final.Bytes)
		}
		if final.Status == "completed" {
			s.convertRecording(final, secs)
		}
	}()
}

// endRunning tells the recording's waiters it is saved.
func (s *Service) endRunning(a *activeRec) {
	s.rec.mu.Lock()
	delete(s.rec.running, a.rec.ID)
	s.rec.mu.Unlock()
	close(a.done)
}

// convertRecording turns a finished MPEG-TS recording into an MP4 (with
// the index at the start, so browsers and TVs can seek in it), then looks
// for advertising breaks. The recording is already saved: if the server
// stops in the middle, the TS stays and the next start converts it.
func (s *Service) convertRecording(r store.Recording, secs float64) {
	s.rec.mu.Lock()
	ctx := s.rec.ctx
	if s.rec.converting[r.ID] || ctx == nil {
		s.rec.mu.Unlock()
		return
	}
	if s.rec.converting == nil {
		s.rec.converting = map[int64]bool{}
	}
	s.rec.converting[r.ID] = true
	s.rec.mu.Unlock()
	defer func() {
		s.rec.mu.Lock()
		delete(s.rec.converting, r.ID)
		s.rec.mu.Unlock()
	}()
	// The MP4 is written next to the TS: without room for a second copy it
	// would fill the disk and cut the recordings running now.
	if fi, err := os.Stat(r.File); err == nil && strings.HasSuffix(r.File, ".ts") && !roomFor(filepath.Dir(r.File), fi.Size()) {
		if s.rec.log != nil {
			s.rec.log.Warn("registrazione lasciata in MPEG-TS: spazio insufficiente per la conversione", "id", r.ID, "mb", fi.Size()>>20)
		}
	} else if strings.HasSuffix(r.File, ".ts") {
		if mp4, err := s.remuxRecording(ctx, r.File, secs); err != nil {
			if s.rec.log != nil && ctx.Err() == nil {
				s.rec.log.Warn("registrazione lasciata in MPEG-TS", "id", r.ID, "err", err)
			}
		} else {
			s.useMP4(r, mp4)
		}
	}
	if r, err := s.st.Recording(context.Background(), r.ID); err == nil && r.Status == "completed" && r.File != "" &&
		!fileExists(edlPath(r.File)) && ctx.Err() == nil {
		s.findCommercials(r.File)
	}
}

// useMP4 points the recording at its MP4 and removes the TS, unless the
// recording was deleted (or its file changed) during the conversion: then
// the MP4 goes.
func (s *Service) useMP4(r store.Recording, mp4 string) {
	var size int64
	if fi, err := os.Stat(mp4); err == nil {
		size = fi.Size()
	}
	s.rec.files.Lock()
	defer s.rec.files.Unlock()
	if ok, err := s.st.SetRecordingFile(context.Background(), r.ID, r.File, mp4, size); err != nil || !ok {
		os.Remove(mp4)
		return
	}
	if fileExists(edlPath(r.File)) {
		os.Rename(edlPath(r.File), edlPath(mp4))
	}
	os.Remove(r.File)
}

func (a *activeRec) size() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bytes
}

// recorded is how many seconds the file holds so far.
func (a *activeRec) recorded() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.secs + a.last.Sub(a.from).Seconds()
}

// offsetAt is where, in bytes, the file reaches the given second
// (negative: close to the live edge), at the start of a TS packet. The
// rate is the average over the time with data: the position is
// approximate, as with a seek in any constant-rate stream.
func (a *activeRec) offsetAt(f *os.File, second float64) int64 {
	return tsOffset(f, a.size(), a.recorded(), second)
}

// tsOffset is offsetAt for a TS file of size bytes and secs seconds.
func tsOffset(f *os.File, size int64, secs, second float64) int64 {
	if size <= 0 || secs <= 1 {
		return 0
	}
	rate := float64(size) / secs
	var off int64
	if second < 0 || second >= secs-liveEdge {
		off = size - int64(rate*liveEdge)
	} else {
		off = int64(rate * second)
	}
	off = max(0, min(off, size-188*3)) / 188 * 188
	return tsSync(f, off)
}

// liveEdge is how far behind the end "live" starts, in seconds: enough to
// reach a picture that can be decoded.
const liveEdge = 4

// tsSync returns the first offset from off where three TS packets in a row
// start (a reconnection may have left the file out of step); off itself if
// none is found nearby.
func tsSync(f *os.File, off int64) int64 {
	buf := make([]byte, 188*64)
	n, _ := f.ReadAt(buf, off)
	for i := 0; i+188*2 < n; i++ {
		if buf[i] == 0x47 && buf[i+188] == 0x47 && buf[i+376] == 0x47 {
			return off + int64(i)
		}
	}
	return off
}

// record writes the channel to the file until ctx ends, reconnecting on
// errors. It returns the last error, if any.
func (s *Service) record(ctx context.Context, a *activeRec) error {
	f, err := os.OpenFile(a.rec.File, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	var gap error // the last interruption: it stays, the file has a hole
	wait, priority, prioritySince := recordRetry, true, time.Time{}
	short, wasBusy := 0, false
	for ctx.Err() == nil {
		it, err := s.st.ChannelForGroup(ctx, a.rec.GroupKey)
		if err != nil {
			return fmt.Errorf("canale non più disponibile: %w", err)
		}
		before, opened := a.size(), time.Now()
		// Back to back on one channel the earlier recording's file already
		// holds these minutes: no viewer of another channel is cut for
		// them. The priority comes back once that recording ends.
		p := priority && !s.earlierRunning(a)
		if p {
			prioritySince = opened
		}
		err = s.recordOnce(ctx, a, it, f, p)
		if ctx.Err() != nil {
			// Reaching the stop time closes the stream: not an error
			// (earlier interruptions still count).
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				gap = err
			}
			break
		}
		gap = err
		if errors.Is(err, errDiskWrite) {
			return err
		}
		busy := errors.As(err, new(ConnectionBusy))
		switch {
		case a.size() > before:
			wait, priority = recordRetry, true
			if time.Since(opened) < shortSession {
				short++
			} else {
				short = 0
			}
			if short == shortSessions {
				s.contended(a, it.SubscriptionID)
			}
		case busy:
			// Nothing was asked of the provider: try again soon, the
			// connection frees up when the recording of the programme
			// before ends or when nobody watches any more. With priority
			// only recordings hold it: the priority stays.
			wait = recordRetry
			priority = priority || time.Since(prioritySince) >= recordPriorityEvery
		default:
			wait = min(wait*2, recordRetryMax)
			priority = time.Since(prioritySince) >= recordPriorityEvery
		}
		if err != nil && s.rec.log != nil && !(busy && wasBusy) {
			s.rec.log.Info("registrazione interrotta: mi ricollego", "id", a.rec.ID, "err", err, "tra", wait)
		}
		wasBusy = busy
		select {
		case <-ctx.Done():
		case <-time.After(wait): // reconnect
		}
	}
	return gap
}

// earlierRunning reports whether a recording of the same channel that
// started before a is still running.
func (s *Service) earlierRunning(a *activeRec) bool {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	for _, o := range s.rec.running {
		if o != a && o.rec.GroupKey == a.rec.GroupKey && o.rec.Start < a.rec.Start {
			return true
		}
	}
	return false
}

// contended notes that the provider keeps closing a recording soon after
// each reconnection.
func (s *Service) contended(a *activeRec, subID int64) {
	a.mu.Lock()
	a.contended = true
	a.mu.Unlock()
	s.rec.mu.Lock()
	if s.rec.contention == nil {
		s.rec.contention = map[int64]contention{}
	}
	s.rec.contention[subID] = contention{title: a.rec.Title, at: time.Now()}
	s.rec.mu.Unlock()
	if s.rec.log != nil {
		s.rec.log.Warn("registrazione interrotta più volte di seguito: forse un altro dispositivo usa l'abbonamento",
			"id", a.rec.ID, "abbonamento", subID)
	}
}

// contentionProblems are the recent recordings cut again and again.
func (s *Service) contentionProblems(ctx context.Context, subName map[int64]string) []Problem {
	var out []Problem
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	for sub, c := range s.rec.contention {
		if time.Since(c.at) > contentionShown || subName[sub] == "" {
			continue
		}
		out = append(out, Problem{Level: "warning", Area: "subscription",
			Text: T(ctx, "La registrazione “%s” è stata interrotta più volte dal provider: forse un altro dispositivo usa l'abbonamento “%s”", c.title, subName[sub])})
	}
	return out
}

// recordOnce records from one provider connection. priority: it may take
// the connection from a viewer.
func (s *Service) recordOnce(ctx context.Context, a *activeRec, it store.PlaybackItem, f *os.File, priority bool) error {
	streamCtx, release, err := s.acquire(ctx, it.SubscriptionID, "recording:"+strconv.FormatInt(a.rec.ID, 10), priority, true)
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
	var pk tsPackets // per connection: a packet cut by its end is dropped
	defer func() {
		a.mu.Lock()
		a.secs += a.last.Sub(a.from).Seconds()
		a.from, a.last = time.Time{}, time.Time{}
		a.mu.Unlock()
	}()
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			last.Store(time.Now().UnixNano())
			if whole := pk.take(buf[:n]); len(whole) > 0 {
				if _, werr := f.Write(whole); werr != nil {
					if s.rec.log != nil {
						s.rec.log.Warn("registrazione: scrittura sul disco non riuscita", "id", a.rec.ID, "err", werr)
					}
					return errDiskWrite
				}
				now := time.Now()
				a.mu.Lock()
				a.bytes += int64(len(whole))
				if a.from.IsZero() {
					a.from = now
				}
				a.last = now
				a.mu.Unlock()
			}
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

// tsPackets passes on only whole 188-byte TS packets, in step with the
// sync byte. A read can end inside a packet, and a connection can end or
// start in the middle of one: written as they came, those pieces put the
// file out of step at every reconnection, and players (mpegts.js) stop
// there.
type tsPackets struct {
	pend   []byte // the start of a packet, until the next read
	synced bool
}

// take returns the whole packets of what was kept plus p.
func (k *tsPackets) take(p []byte) []byte {
	b := append(k.pend, p...)
	out := make([]byte, 0, len(b))
	i := 0
	for len(b)-i >= 188 {
		if b[i] != 0x47 {
			k.synced, i = false, i+1
			continue
		}
		if !k.synced {
			// A 0x47 can be inside a packet too: a new start counts only
			// when the next packet starts where it should.
			if len(b)-i < 376 {
				break
			}
			if b[i+188] != 0x47 {
				i++
				continue
			}
		}
		k.synced = true
		out = append(out, b[i:i+188]...)
		i += 188
	}
	k.pend = append([]byte(nil), b[i:]...)
	return out
}

// ---- Scheduling ----

// programmeCore is the part of a guide programme that the recordings of
// the programmes before and after it do not reach with their margins: on
// one channel, programmes back to back overlap only there, and both are
// recorded (with one connection the second starts when the first ends; its
// first minutes are at the end of the first file).
func programmeCore(start, stop int64) (int64, int64) {
	from, to := start+int64(recordAfter/time.Second), stop-int64(recordBefore/time.Second)
	if to <= from {
		from = (start + stop) / 2
		to = from + 1
	}
	return from, to
}

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
	from, to := start, stop
	if margins {
		from, to = programmeCore(start+int64(recordBefore/time.Second), stop-int64(recordAfter/time.Second))
	}
	if other, err := s.st.OverlappingRecording(ctx, group, from, to); err != nil {
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
		list[i].Error = TranslateText(LangOf(ctx), list[i].Error) // saved in Italian
	}
	s.rec.mu.Unlock()
	return list, err
}

// StopRecording ends a running recording now and keeps what it has
// recorded, as a finished recording.
func (s *Service) StopRecording(ctx context.Context, id int64) error {
	s.rec.mu.Lock()
	a := s.rec.running[id]
	s.rec.mu.Unlock()
	if a == nil {
		return invalid("la registrazione non è in corso")
	}
	a.mu.Lock()
	a.early = true
	a.mu.Unlock()
	a.cancel()
	select {
	case <-a.done: // closed and saved (the conversion to MP4 follows)
	case <-ctx.Done():
	}
	return nil
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
	s.rec.files.Lock()
	defer s.rec.files.Unlock()
	// Read again: it may have just finished, or become an MP4.
	if r, err = s.st.Recording(ctx, id); err != nil {
		return err
	}
	if r.File != "" {
		if err := os.Remove(r.File); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		os.Remove(edlPath(r.File))
	}
	if r.RuleID != 0 {
		// Its rule must not schedule it again, also when it was stopped
		// early (Stop is then already past) and the programme is still on
		// air. PurgeCancelled drops the marker a week later.
		return s.st.CancelRecording(ctx, id)
	}
	return s.st.DeleteRecording(ctx, id)
}

// ---- Watching recordings ----

// activeRecordingFor returns the running recording of the channel an item
// belongs to, if any. Back to back, the one with the most data: the next
// one may still be waiting for the connection with an empty file.
func (s *Service) activeRecordingFor(ctx context.Context, itemID int64) *activeRec {
	group, err := s.st.GroupKeyOf(ctx, itemID)
	if err != nil {
		return nil
	}
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	var best *activeRec
	for _, a := range s.rec.running {
		if a.rec.GroupKey == group && (best == nil || a.size() > best.size()) {
			best = a
		}
	}
	return best
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
		// In step even in a file written before whole packets were kept.
		f.Seek(tsSync(f, (size-back)/188*188), io.SeekStart)
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

// RecordingLength says how many seconds a recording holds (so far, when
// running) and whether its file is MPEG-TS: players move in a TS by
// reopening it at a second (?t=), as in a recording in progress.
func (s *Service) RecordingLength(ctx context.Context, id int64) (secs float64, ts, running bool, err error) {
	s.rec.mu.Lock()
	a := s.rec.running[id]
	s.rec.mu.Unlock()
	if a != nil {
		return a.recorded(), true, true, nil
	}
	r, err := s.st.Recording(ctx, id)
	if err != nil {
		return 0, false, false, err
	}
	return finishedLength(r), strings.HasSuffix(r.File, ".ts"), false, nil
}

// finishedLength estimates how long a finished recording is: from when it
// started (a recording scheduled for a programme already on air starts
// when it was asked for) to its real end, which is also the last write of
// its file.
func finishedLength(r store.Recording) float64 {
	start, stop := max(r.Start, r.CreatedAt), r.Stop
	if fi, err := os.Stat(r.File); err == nil {
		stop = min(stop, fi.ModTime().Unix())
	}
	return float64(max(0, stop-start))
}

// OpenRecording returns the recording file (finished: seekable) or, if it
// is still being recorded, a stream that follows it from the given second
// (negative: from the live edge). A finished TS asked from a second is a
// stream from there too.
func (s *Service) OpenRecording(ctx context.Context, ticket string, from float64) (*os.File, *Stream, store.Recording, error) {
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
		if from != 0 {
			if _, err := f.Seek(a.offsetAt(f, from), io.SeekStart); err != nil {
				f.Close()
				return nil, nil, r, err
			}
		}
		return nil, &Stream{ReadCloser: &follower{f: f, ctx: ctx, a: a}, ContentType: "video/mp2t", Title: r.Title}, r, nil
	}
	if r.File == "" {
		return nil, nil, r, ErrNotReady
	}
	f, err := os.Open(r.File)
	if err != nil || from == 0 || !strings.HasSuffix(r.File, ".ts") {
		return f, nil, r, err
	}
	// A finished TS, from a second: players cannot seek far in one.
	fi, err := f.Stat()
	if err == nil {
		_, err = f.Seek(tsOffset(f, fi.Size(), finishedLength(r), from), io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, nil, r, err
	}
	return nil, &Stream{ReadCloser: f, ContentType: "video/mp2t", Title: r.Title}, r, nil
}

// ErrNotReady means the recording has no file yet.
var ErrNotReady = errNotReady

// remuxRecording copies a finished MPEG-TS recording into an MP4 with the
// index at the start, so browsers can seek in it. The video is never
// re-encoded. secs is how long the recording is (0: not known).
func (s *Service) remuxRecording(ctx context.Context, ts string, secs float64) (string, error) {
	mp4 := strings.TrimSuffix(ts, ".ts") + ".mp4"
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	err := s.ffmpegToMP4(ctx, ts, mp4, "-c", "copy")
	if err != nil && ctx.Err() == nil && !errors.Is(err, exec.ErrNotFound) {
		// One broken audio frame, normal in a provider's stream, stops the
		// copy (as at 22:05 of 05/11): the audio is converted instead.
		if s.rec.log != nil {
			s.rec.log.Info("conversione in MP4: la copia non è riuscita, riprovo convertendo l'audio", "file", filepath.Base(ts), "err", err)
		}
		err = s.ffmpegToMP4(ctx, ts, mp4, "-c:v", "copy", "-c:a", "aac", "-b:a", "160k")
	}
	if err == nil {
		err = s.incompleteMP4(ctx, ts, mp4, secs)
	}
	if err != nil {
		os.Remove(mp4)
		return "", err
	}
	return mp4, nil
}

func (s *Service) ffmpegToMP4(ctx context.Context, ts, mp4 string, codecs ...string) error {
	args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", ts, "-map", "0:v:0", "-map", "0:a?"}, codecs...)
	cmd := exec.CommandContext(ctx, s.ffmpegBin(), append(args, "-movflags", "+faststart", mp4)...)
	var e errorLine
	cmd.Stderr = &e
	if err := cmd.Run(); err != nil {
		if line := e.line(); line != "" {
			return fmt.Errorf("%s (%w)", line, err)
		}
		return err
	}
	return nil
}

// incompleteMP4 tells whether an MP4 cannot replace its TS: ffmpeg can end
// without an error after copying only part of a file, and the TS would go
// with the only complete copy. The MP4 must be at least half the TS and,
// when ffprobe can tell and the length is known, last at least 80% of it.
func (s *Service) incompleteMP4(ctx context.Context, ts, mp4 string, secs float64) error {
	t, err1 := os.Stat(ts)
	m, err2 := os.Stat(mp4)
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	if m.Size() < t.Size()/2 {
		return fmt.Errorf("file MP4 incompleto: %d MB su %d MB", m.Size()>>20, t.Size()>>20)
	}
	if secs <= 0 {
		return nil
	}
	out, err := exec.CommandContext(ctx, s.ffprobeBin(), "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", mp4).Output()
	if err != nil {
		return nil // no ffprobe: the size has to do
	}
	if d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err == nil && d < secs*0.8 {
		return fmt.Errorf("file MP4 incompleto: %.0f s su %.0f s", d, secs)
	}
	return nil
}

// errorLine keeps, of ffmpeg's messages, the first line that names an
// error ("aac_adtstoasc: Error parsing ADTS frame header!"), otherwise the
// first one: alone, the first is often a harmless decoder warning.
type errorLine struct {
	part        []byte // the line being written
	first, kept string
}

func (e *errorLine) Write(p []byte) (int, error) {
	e.part = append(e.part, p...)
	for {
		i := bytes.IndexByte(e.part, '\n')
		if i < 0 {
			break
		}
		e.add(string(e.part[:i]))
		e.part = e.part[i+1:]
	}
	if len(e.part) > 4096 {
		e.add(string(e.part))
		e.part = nil
	}
	return len(p), nil
}

func (e *errorLine) add(line string) {
	line = strings.TrimSpace(line)
	if e.first == "" {
		e.first = line
	}
	if e.kept == "" && strings.Contains(line, "Error") {
		e.kept = line
	}
}

func (e *errorLine) line() string {
	e.add(string(e.part))
	e.part = nil
	if e.kept != "" {
		return e.kept
	}
	return e.first
}

// convertLeftovers converts the finished recordings still in MPEG-TS: left
// so by an older version, by a failed conversion or by a restart in the
// middle of one. It runs once at each start.
func (s *Service) convertLeftovers(ctx context.Context) {
	list, err := s.st.RecordingsInState(ctx, "completed")
	if err != nil {
		return
	}
	for _, r := range list {
		if ctx.Err() != nil {
			return
		}
		if strings.HasSuffix(r.File, ".ts") && fileExists(r.File) {
			s.convertRecording(r, 0)
		}
	}
}
