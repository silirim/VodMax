package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
)

// A download holds the only connection until someone wants to watch: then
// it gives it up at once, and cannot take it back while they watch.
func TestBackgroundGivesWay(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	s := New(st, box, nil)
	vpnID, _ := st.AddVPN(ctx, "v", "system", func(int64) []byte { return []byte("x") })
	sub, _ := st.AddSubscription(ctx, store.Subscription{Name: "B", Kind: "xtream", VPNID: vpnID, Settings: json.RawMessage(`{}`)},
		func(int64) []byte { return []byte("x") })
	st.SetSyncResult(ctx, sub, "ok", "", json.RawMessage(`{"max_connections":1}`))

	bg, release, err := s.acquireBackground(ctx, sub, "download:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.acquireBackground(ctx, sub, "download:2"); !errors.As(err, new(ConnectionBusy)) {
		t.Fatalf("second download: %v", err)
	}
	go func() { <-bg.Done(); release() }()
	_, watch, err := s.acquire(ctx, sub, "salotto-tv-01", false, false)
	if err != nil {
		t.Fatalf("viewer refused because of a download: %v", err)
	}
	select {
	case <-bg.Done():
	case <-time.After(time.Second):
		t.Fatal("the download kept the connection")
	}
	if _, _, err := s.acquireBackground(ctx, sub, "download:1"); !errors.As(err, new(ConnectionBusy)) {
		t.Fatalf("download took the connection back: %v", err)
	}
	watch()
}

// A stream taken over while it starts hears what it would hear asking
// again with force: "take control?" while a viewer holds a connection,
// the recording message when only recordings are left, since nobody can
// take one back from them.
func TestTakenOverHearsBusy(t *testing.T) {
	ctx := context.Background()
	hold := func(s *Service, sub int64, viewer string, force, recording bool) context.Context {
		t.Helper()
		c, release, err := s.acquire(ctx, sub, viewer, force, recording)
		if err != nil {
			t.Fatalf("%s: %v", viewer, err)
		}
		go func() { <-c.Done(); release() }()
		t.Cleanup(release)
		return c
	}
	heard := func(c context.Context) ConnectionBusy {
		t.Helper()
		var busy ConnectionBusy
		if !errors.As(context.Cause(c), &busy) {
			t.Fatalf("taken over: %v, want busy", context.Cause(c))
		}
		return busy
	}

	s, sub := oneConnectionService(t)
	film := hold(s, sub, "film-viewer-1", false, false)
	tv := hold(s, sub, "tv-viewer-02", true, false)
	if heard(film).Recording {
		t.Error("taken by a viewer: the recording message")
	}
	hold(s, sub, "recording:1", true, true)
	if !heard(tv).Recording {
		t.Error("taken by a recording: \"take control?\" that cannot work")
	}
	if _, _, err := s.acquire(ctx, sub, "tv-viewer-02", true, false); !errors.Is(err, ConnectionBusy{Max: 1, Recording: true}) {
		t.Errorf("asking again with force: %v", err)
	}

	// Two connections: the recording takes one, a viewer keeps the other.
	s, sub = oneConnectionService(t)
	s.st.SetSyncResult(ctx, sub, "ok", "", json.RawMessage(`{"max_connections":2}`))
	film = hold(s, sub, "film-viewer-1", false, false)
	hold(s, sub, "tv-viewer-02", false, false)
	hold(s, sub, "recording:1", true, true)
	if heard(film).Recording {
		t.Error("a viewer still holds a connection: the recording message")
	}
}
