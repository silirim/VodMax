package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInterruptedDue(t *testing.T) {
	now := time.Now()
	cases := []struct {
		run  syncRun
		want bool
	}{
		{syncRun{}, true},
		{syncRun{Started: now.Add(-time.Minute), Attempts: 1}, false},
		{syncRun{Started: now.Add(-31 * time.Minute), Attempts: 1}, true},
		{syncRun{Started: now.Add(-31 * time.Minute), Attempts: 2}, false},
		{syncRun{Started: now.Add(-61 * time.Minute), Attempts: 2}, true},
		{syncRun{Started: now.Add(-48 * time.Hour), Attempts: maxInterrupted}, false},
	}
	for _, c := range cases {
		if got := interruptedDue(c.run, now); got != c.want {
			t.Errorf("%+v: got %v", c.run, got)
		}
	}
}

// waitView waits until the view of the first subscription satisfies ok.
func waitView(t *testing.T, s *Service, ok func(SubscriptionView) bool) SubscriptionView {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		subs, err := s.Subscriptions(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if ok(subs[0]) {
			return subs[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("view: %+v", subs[0])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A sync left running by a process that ended (Android stopped the app's
// server) shows as interrupted, and the maintenance does not start it
// again at every start: after a pause, and a few times at most. The
// person still can.
func TestInterruptedSync(t *testing.T) {
	ctx := context.Background()
	s, vpnID := linkService(t)
	var hits atomic.Int32
	var hold, holdGone atomic.Bool
	release := make(chan struct{})
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/player_api.php" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("action") {
		case "":
			w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","exp_date":"1893456000","max_connections":"1","active_cons":"0"}}`))
		case "get_live_streams":
			if hold.Load() {
				<-release
			}
			if holdGone.Load() {
				<-r.Context().Done() // until the client gives up
				return
			}
			w.Write([]byte(`[{"num":1,"name":"Canale Uno","stream_id":101,"category_id":"1"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(panel.Close)
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free) // before panel.Close, which waits for a held answer
	id, err := s.AddSubscription(ctx, SubscriptionInput{Name: "A", Kind: "xtream", VPNID: vpnID, Server: panel.URL, Username: "mario", Password: "segreta"})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The process ended a minute after the first sync started.
	s.st.SetSetting(ctx, syncRunKey(id), syncRun{Started: time.Now().Add(-time.Minute), Attempts: 1})
	s.runScheduled(ctx, log, time.Now())
	if hits.Load() != 0 {
		t.Fatalf("started again at once: %d requests", hits.Load())
	}
	if v := waitView(t, s, func(SubscriptionView) bool { return true }); !v.SyncInterrupted || v.Syncing {
		t.Fatalf("not shown as interrupted: %+v", v)
	}
	// From any page, since the app opens on the empty home.
	if st, err := s.Status(ctx); err != nil || !slices.ContainsFunc(st.Problems, func(p Problem) bool {
		return p.Area == "subscription" && strings.Contains(p.Text, "si è interrotta")
	}) {
		t.Fatalf("status: %+v %v", st.Problems, err)
	}

	// After the pause it is tried again, and the mark goes with its end.
	s.runScheduled(ctx, log, time.Now().Add(31*time.Minute))
	if hits.Load() == 0 {
		t.Fatal("not tried again after the pause")
	}
	if v := waitView(t, s, func(SubscriptionView) bool { return true }); v.SyncInterrupted || v.LastSync == nil || v.LastSync.Error != "" || v.LastSync.Total["live"] != 1 {
		t.Fatalf("after the retry: %+v %+v", v, v.LastSync)
	}

	// Cut short maxInterrupted times: the next night does not try.
	s.st.SetSetting(ctx, syncRunKey(id), syncRun{Started: time.Now().Add(-48 * time.Hour), Attempts: maxInterrupted})
	before := hits.Load()
	s.runScheduled(ctx, log, time.Now().Add(48*time.Hour))
	if hits.Load() != before {
		t.Fatal("tried again after maxInterrupted attempts")
	}

	// The person starts it: it runs in the background with its phase, and
	// counts as one more attempt until it ends.
	hold.Store(true)
	v, err := s.StartSync(ctx, id, false)
	if err != nil || !v.Syncing {
		t.Fatalf("start: %+v %v", v, err)
	}
	v = waitView(t, s, func(v SubscriptionView) bool { return v.SyncPhase == "Scarico i canali" })
	var run syncRun
	s.st.Setting(ctx, syncRunKey(id), &run)
	if !v.Syncing || v.SyncInterrupted || run.Attempts != maxInterrupted+1 {
		t.Fatalf("while running: %+v, mark %+v", v, run)
	}
	if again, err := s.StartSync(ctx, id, false); err != nil || !again.Syncing {
		t.Fatalf("second start: %+v %v", again, err)
	}
	// Its catalog would land in a new subscription given the same id.
	if err := s.DeleteSubscription(ctx, id); !errors.Is(err, ErrBusy) {
		t.Fatalf("deleted while syncing: %v", err)
	}
	free()
	v = waitView(t, s, func(v SubscriptionView) bool { return !v.Syncing })
	if v.SyncInterrupted || v.LastSync == nil || v.LastSync.Error != "" {
		t.Fatalf("after the end: %+v %+v", v, v.LastSync)
	}

	// A guide cut short by the end of the process (the catalog is in)
	// waits as well, instead of running at every start.
	s.mu.Lock()
	clear(s.maint.guideRetry) // the fake panel has no guide
	s.mu.Unlock()
	s.st.SetSetting(ctx, guideRunKey(id), syncRun{Started: time.Now().Add(-time.Minute), Attempts: 1})
	before = hits.Load()
	s.runScheduled(ctx, log, time.Now())
	if hits.Load() != before {
		t.Fatal("guide started again at once")
	}
	s.st.SetSetting(ctx, guideRunKey(id), syncRun{Started: time.Now().Add(-31 * time.Minute), Attempts: 1})
	s.runScheduled(ctx, log, time.Now())
	if hits.Load() == before {
		t.Fatal("guide not tried again after the pause")
	}
	var left syncRun
	if s.st.Setting(ctx, guideRunKey(id), &left); left.Attempts != 0 {
		t.Fatalf("guide mark left: %+v", left)
	}

	// The process stops in the middle of a sync: no error is saved, and
	// the sync shows as interrupted after the restart.
	holdGone.Store(true)
	if _, err := s.StartSync(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	waitView(t, s, func(v SubscriptionView) bool { return v.SyncPhase == "Scarico i canali" })
	s.endLife()
	if v = waitView(t, s, func(v SubscriptionView) bool { return !v.Syncing }); !v.SyncInterrupted || v.SyncStatus != "ok" {
		t.Fatalf("after the stop: %+v", v)
	}
}

// New titles stay hidden until the selection that follows the import: a
// process stopped during it leaves the sync interrupted, not done, and
// maintenance does it again after the pause, also for a sync started
// during the day. A selection that fails on its own is saved as a failed
// sync, retried after syncRetry.
func TestSyncInterruptedInSelection(t *testing.T) {
	ctx := context.Background()
	s, vpnID := linkService(t)
	// The nightly hour is never close: only the interruption makes the
	// sync due.
	nightly := Schedule{CatalogHour: (time.Now().Hour() + 12) % 24, GuideHours: 6}
	if err := s.SetSchedule(ctx, nightly); err != nil {
		t.Fatal(err)
	}
	var catalogHits atomic.Int32
	var second atomic.Bool
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/player_api.php" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("action") {
		case "":
			w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","exp_date":"1893456000","max_connections":"1","active_cons":"0"}}`))
		case "get_live_streams":
			catalogHits.Add(1)
			if second.Load() {
				w.Write([]byte(`[{"num":1,"name":"Canale Uno","stream_id":101,"category_id":"1"},{"num":2,"name":"Canale Due","stream_id":102,"category_id":"1"}]`))
				return
			}
			w.Write([]byte(`[{"num":1,"name":"Canale Uno","stream_id":101,"category_id":"1"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(panel.Close)
	id, err := s.AddSubscription(ctx, SubscriptionInput{Name: "A", Kind: "xtream", VPNID: vpnID, Server: panel.URL, Username: "mario", Password: "segreta"})
	if err != nil {
		t.Fatal(err)
	}

	// A sync stopped during the selection, and the restart: a new process
	// on the same database.
	stopInSelection := func() {
		t.Helper()
		sel := &s.selMu // the selection waits until the process has stopped
		sel.Lock()
		var once sync.Once
		unlock := func() { once.Do(sel.Unlock) }
		t.Cleanup(unlock)
		if _, err := s.StartSync(ctx, id, false); err != nil {
			t.Fatal(err)
		}
		waitView(t, s, func(v SubscriptionView) bool { return v.SyncPhase == "Applico la selezione" })
		s.endLife()
		unlock()
		waitView(t, s, func(v SubscriptionView) bool { return !v.Syncing })
		s = New(s.st, s.box, nil)
	}
	stopInSelection()
	v := waitView(t, s, func(SubscriptionView) bool { return true })
	if !v.SyncInterrupted || v.SyncedAt != nil || v.Counts["live"] != 0 {
		t.Fatalf("after the stop: %+v", v)
	}
	if st, err := s.Status(ctx); err != nil || !slices.ContainsFunc(st.Problems, func(p Problem) bool {
		return p.Area == "subscription" && strings.Contains(p.Text, "si è interrotta")
	}) {
		t.Fatalf("status: %+v %v", st.Problems, err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.runScheduled(ctx, log, time.Now().Add(31*time.Minute))
	if catalogHits.Load() != 2 {
		t.Fatalf("catalog read %d times", catalogHits.Load())
	}
	v = waitView(t, s, func(SubscriptionView) bool { return true })
	if v.SyncInterrupted || v.SyncedAt == nil || v.SyncStatus != "ok" || v.Counts["live"] != 1 {
		t.Fatalf("after the retry: %+v", v)
	}

	// A later sync, started by hand during the day: its old synced_at is
	// past the nightly hour, and the new channel waits for the retry.
	synced := *v.SyncedAt
	second.Store(true)
	stopInSelection()
	if v = waitView(t, s, func(SubscriptionView) bool { return true }); !v.SyncInterrupted || v.SyncedAt == nil || !v.SyncedAt.Equal(synced) || v.Counts["live"] != 1 {
		t.Fatalf("after the second stop: %+v", v)
	}
	// With the automatic sync off only the person starts it.
	if err := s.SetSchedule(ctx, Schedule{CatalogHour: -1, GuideHours: 6}); err != nil {
		t.Fatal(err)
	}
	s.runScheduled(ctx, log, time.Now().Add(31*time.Minute))
	if catalogHits.Load() != 3 {
		t.Fatalf("catalog read %d times with the automatic sync off", catalogHits.Load())
	}
	if err := s.SetSchedule(ctx, nightly); err != nil {
		t.Fatal(err)
	}
	s.runScheduled(ctx, log, time.Now().Add(31*time.Minute))
	if catalogHits.Load() != 4 {
		t.Fatalf("catalog read %d times", catalogHits.Load())
	}
	if v = waitView(t, s, func(SubscriptionView) bool { return true }); v.SyncInterrupted || v.SyncStatus != "ok" || v.Counts["live"] != 2 {
		t.Fatalf("after the second retry: %+v", v)
	}

	if err := s.st.SetRules(ctx, id, json.RawMessage(`{"home":5}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(ctx, id, false); err == nil {
		t.Fatal("sync with unreadable rules")
	}
	var run syncRun
	s.st.Setting(ctx, syncRunKey(id), &run)
	if v = waitView(t, s, func(SubscriptionView) bool { return true }); v.SyncStatus != "error" || v.SyncError == "" || run.Attempts != 0 {
		t.Fatalf("after a failed selection: %+v, mark %+v", v, run)
	}
}

// A collection, too, is done only once the selection shows its titles: a
// selection that fails leaves it failed, not synced.
func TestLocalSyncFailedSelection(t *testing.T) {
	ctx := context.Background()
	s, _ := linkService(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Film di casa.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := s.AddSubscription(ctx, SubscriptionInput{Name: "Casa", Kind: "local", Path: dir, Content: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.st.SetRules(ctx, id, json.RawMessage(`{"home":5}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(ctx, id, false); err == nil {
		t.Fatal("sync with unreadable rules")
	}
	if v := waitView(t, s, func(SubscriptionView) bool { return true }); v.SyncStatus != "error" || v.SyncError == "" {
		t.Fatalf("after a failed selection: %+v", v)
	}
}
