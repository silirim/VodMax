package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
)

// A TMDB failure is not retried at every home page: the next try comes
// after trendingRetry, and the list that worked before stays.
func TestTrendingBacksOffAfterAFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	s := New(st, box, nil)
	var calls atomic.Int32
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if down.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"results":[{"id":539,"title":"Psyco","release_date":"1960-06-16"}]}`))
	}))
	defer srv.Close()
	s.SetTMDBBase(srv.URL)
	if err := s.setSecretSetting(ctx, tmdbKeySetting, "chiave"); err != nil {
		t.Fatal(err)
	}
	down.Store(true)
	s.Trending(ctx)
	failed := calls.Load()
	s.Trending(ctx)
	if calls.Load() != failed {
		t.Fatalf("retried at once: %d → %d calls", failed, calls.Load())
	}
	// After the retry delay TMDB answers again.
	down.Store(false)
	s.trend.at = s.trend.at.Add(-trendingRetry)
	s.Trending(ctx)
	if len(s.trend.list["movie"]) != 2 { // two pages of the same answer
		t.Fatalf("list after the retry: %+v", s.trend.list)
	}
	// A later failure keeps that list.
	down.Store(true)
	s.trend.at = time.Now().Add(-trendingFor - time.Minute)
	s.Trending(ctx)
	if len(s.trend.list["movie"]) != 2 || time.Since(s.trend.at) > trendingFor-trendingRetry+time.Minute {
		t.Fatalf("list lost or no back-off: %+v %v", s.trend.list, s.trend.at)
	}
}
