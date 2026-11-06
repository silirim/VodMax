package service

import (
	"context"
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
