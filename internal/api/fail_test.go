package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/silirim/vodmax/internal/service"
	"github.com/silirim/vodmax/internal/store"
)

// An unexpected failure shows its detail (provider address, SQL, paths)
// only to an admin; others get a generic text, people's messages stay.
func TestFailHidesInternalDetail(t *testing.T) {
	a := &api{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	kids := service.Identity{DeviceID: 3, Profile: &store.Profile{ID: 2, Kind: "kids"}}
	admin := service.Identity{DeviceID: 3, Profile: &store.Profile{ID: 1, Kind: "admin", HasPIN: true}}
	leak := errors.New(`provider: Get "http://provider.example:8080/…": dial tcp: connection refused`)
	cases := []struct {
		id       *service.Identity
		err      error
		want     string
		withheld bool
	}{
		{nil, leak, "provider.example", true}, // a /stream ticket: no identity
		{&kids, leak, "provider.example", true},
		{&admin, leak, "provider.example", false},
		{&kids, errors.New("il canale non trasmette"), "il canale non trasmette", false},
	}
	for i, c := range cases {
		r := httptest.NewRequest("GET", "/stream/live/x", nil)
		if c.id != nil {
			r = r.WithContext(context.WithValue(r.Context(), identityKey{}, *c.id))
		}
		w := httptest.NewRecorder()
		a.fail(w, r, c.err)
		body := w.Body.String()
		if w.Code != 500 || strings.Contains(body, c.want) == c.withheld {
			t.Errorf("case %d: %d %s", i, w.Code, body)
		}
		if c.withheld && !strings.Contains(body, "registro") {
			t.Errorf("case %d: no generic text: %s", i, body)
		}
	}
}

// The per-client limits trust a forwarded address only from a tunnel on
// the same host; a client in the house cannot pick its own.
func TestClientAddr(t *testing.T) {
	cases := []struct{ remote, cf, xff, want string }{
		{"192.168.1.20:5000", "", "", "192.168.1.20"},
		{"192.168.1.20:5000", "203.0.113.9", "198.51.100.1", "192.168.1.20"},
		{"127.0.0.1:5000", "203.0.113.9", "198.51.100.1", "203.0.113.9"},
		{"[::1]:5000", "", "10.9.9.9, 198.51.100.1", "198.51.100.1"},
		{"127.0.0.1:5000", "", "", "127.0.0.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/api/v1/pair", nil)
		r.RemoteAddr = c.remote
		if c.cf != "" {
			r.Header.Set("CF-Connecting-IP", c.cf)
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientAddr(r); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}

// Logos: a client that went away is not a server error, and a source
// that cannot be reached is 502, not a 500 logged as an error.
func TestFailQuietForImages(t *testing.T) {
	var logged strings.Builder
	a := &api{log: slog.New(slog.NewTextHandler(&logged, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	a.fail(w, httptest.NewRequest("GET", "/img/logo/1", nil).WithContext(ctx), context.Canceled)
	if w.Body.Len() != 0 || logged.Len() != 0 {
		t.Fatalf("client gone: %d %q, log %q", w.Code, w.Body.String(), logged.String())
	}
	w = httptest.NewRecorder()
	a.fail(w, httptest.NewRequest("GET", "/img/logo/1", nil), service.ErrImageUnreachable)
	if w.Code != 502 || logged.Len() != 0 {
		t.Fatalf("unreachable: %d, log %q", w.Code, logged.String())
	}
	// An empty guide is the provider's answer, kept out (the old one stays).
	w = httptest.NewRecorder()
	a.fail(w, httptest.NewRequest("POST", "/api/v1/guide/sources/1/sync", nil), store.ErrGuideEmpty)
	if w.Code != 502 || logged.Len() != 0 {
		t.Fatalf("empty guide: %d, log %q", w.Code, logged.String())
	}
}
