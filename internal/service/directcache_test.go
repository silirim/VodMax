package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
)

// oneConnectionService has a subscription limited to one connection, on
// the normal network.
func oneConnectionService(t *testing.T) (*Service, int64) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, box, nil)
	if err := s.SetCacheDir(filepath.Join(dir, "cache")); err != nil {
		t.Fatal(err)
	}
	seal := func(int64) []byte { return []byte("x") }
	vpnID, err := st.AddVPN(ctx, "v", "system", seal)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.AddSubscription(ctx, store.Subscription{Name: "B", Kind: "xtream", VPNID: vpnID, Settings: json.RawMessage(`{}`)}, seal)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSyncResult(ctx, sub, "ok", "", json.RawMessage(`{"max_connections":1}`))
	return s, sub
}

// slowFile serves a file of size bytes slowly, with ranges.
func slowFile(t *testing.T, size int) *httptest.Server {
	t.Helper()
	file := make([]byte, size)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var from int64
		fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &from)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(file)-1, len(file)))
		w.WriteHeader(http.StatusPartialContent)
		for off := from; off < int64(len(file)); off += 64 << 10 {
			if _, err := w.Write(file[off:min(off+64<<10, int64(len(file)))]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	return srv
}

// After "take control" on a direct film, someone else taking control
// keeps the connection: the film's cache does not take it back.
func TestForcedDirectFilmDoesNotTakeBack(t *testing.T) {
	s, sub := oneConnectionService(t)
	srv := slowFile(t, 64<<20)
	sess := &vodSession{id: "sessaaaa", viewer: "film-viewer-1", subID: sub, url: srv.URL, mode: "direct", expires: time.Now().Add(time.Hour)}
	cache, err := s.directCache(sess, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	read := make(chan error, 1)
	go func() { read <- cache.WriteRange(ctx, io.Discard, 0, 64<<20-1) }()
	time.Sleep(500 * time.Millisecond)

	tvCtx, tvRelease, err := s.acquire(context.Background(), sub, "tv-viewer-02", true, false)
	if err != nil {
		t.Fatalf("take control refused: %v", err)
	}
	defer tvRelease()
	select {
	case <-tvCtx.Done():
		t.Fatal("the film took the connection back")
	case err := <-read:
		// The film asks like anyone else: busy, so the player can offer
		// to take control.
		if !errors.As(err, new(ConnectionBusy)) {
			t.Fatalf("film read ended with %v, want busy", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("film read neither ended nor gave way")
	}
}

// Every range of a forced film repeats ?force=1: a healthy cache stays,
// so reads in flight go on.
func TestForceOnEveryRangeKeepsTheCache(t *testing.T) {
	s, sub := oneConnectionService(t)
	srv := slowFile(t, 8<<20)
	sess := &vodSession{id: "sessbbbb", viewer: "film-viewer-1", subID: sub, url: srv.URL, mode: "direct", expires: time.Now().Add(time.Hour)}
	c1, err := s.directCache(sess, true)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	read := make(chan error, 1)
	go func() { read <- c1.WriteRange(context.Background(), io.Discard, 0, 4<<20-1) }()
	time.Sleep(300 * time.Millisecond)
	if c2, _ := s.directCache(sess, true); c2 != c1 {
		t.Fatal("a forced range replaced a healthy cache")
	}
	if err := <-read; err != nil {
		t.Fatalf("first range: %v", err)
	}
}
