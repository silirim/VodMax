package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Provider clients are built per request: once a logo or a range has
// been read, no idle connection may stay open behind it.
func TestProviderRequestsLeaveNoIdleConnections(t *testing.T) {
	var open atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...))
	}))
	srv.Config.ConnState = func(c net.Conn, st http.ConnState) {
		switch st {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	srv.Start()
	defer srv.Close()
	s, sub := oneConnectionService(t)
	s.imageKey() // sets up the fetch slots, as cachedImage does
	// A tunnel that reaches the test server on loopback, which the normal
	// network refuses for images.
	info, err := s.st.Subscription(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.tunnels[info.VPNID] = loopbackNet{}
	s.mu.Unlock()
	for i := 0; i < 10; i++ {
		if _, err := s.fetchImage(context.Background(), sub, srv.URL+fmt.Sprintf("/logo/%d.png", i)); err != nil {
			t.Fatal(err)
		}
		resp, err := s.upstream(context.Background(), sub, srv.URL+"/movie/1.mp4", "bytes=0-")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for open.Load() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d idle provider connections left open", open.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type loopbackNet struct{ systemNetwork }

// A playlist's logo on this machine's loopback (or a redirect to it) is
// not fetched over the normal network, and is remembered as no image.
func TestImagesRefuseLoopback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	s, sub := oneConnectionService(t)
	s.imageKey()
	if _, err := s.fetchImage(context.Background(), sub, srv.URL+"/admin/snapshot.jpg"); !errors.Is(err, ErrNoImage) {
		t.Fatalf("loopback image: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("loopback reached")
	}
}
