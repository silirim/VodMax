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
