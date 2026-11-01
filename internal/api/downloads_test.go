package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type downloadRow struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Bytes  int64  `json:"bytes"`
	Error  string `json:"error"`
}

func waitDownloads(t *testing.T, e *env, n int) []downloadRow {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, body := e.do(t, "GET", "/api/v1/downloads", nil, true)
		var list []downloadRow
		json.Unmarshal(body, &list)
		done := 0
		for _, d := range list {
			if d.Status == "done" {
				done++
			}
		}
		if len(list) == n && done == n {
			return list
		}
		if time.Now().After(deadline) {
			t.Fatalf("downloads not done: %s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestDownloads(t *testing.T) {
	e, p := tvSetup(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := e.svc.StartDownloads(ctx, dir, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	film := findItem(t, e, "movie", "1408")
	// A previous, interrupted attempt: the download resumes from it.
	movie := filepath.Join(dir, "Movies", "1408 (2007)", "1408 (2007).mp4")
	os.MkdirAll(filepath.Dir(movie), 0o755)
	os.WriteFile(movie+".part", fakeFilm[:1000], 0o644)

	code, body := e.do(t, "POST", "/api/v1/downloads", map[string]any{"media": "movie", "id": film}, true)
	if code != 201 || !strings.Contains(string(body), `"added":1`) {
		t.Fatalf("add: %d %s", code, body)
	}
	if _, body := e.do(t, "POST", "/api/v1/downloads", map[string]any{"media": "movie", "id": film}, true); !strings.Contains(string(body), `"already":1`) {
		t.Fatalf("again: %s", body)
	}
	waitDownloads(t, e, 1)
	got, err := os.ReadFile(movie)
	if err != nil || !bytes.Equal(got, fakeFilm) {
		t.Fatalf("film file: %v (%d bytes)", err, len(got))
	}
	if !strings.Contains(strings.Join(p.ranges, ","), "bytes=1000-") {
		t.Fatalf("not resumed: %v", p.ranges)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(movie), "1408 (2007).nfo")); err != nil {
		t.Fatal("no .nfo next to the film")
	}
	// The downloaded copy is a collection, preferred to the provider's.
	_, body = e.do(t, "GET", "/api/v1/subscriptions", nil, true)
	if !strings.Contains(string(body), "Scaricati · Film") {
		t.Fatalf("collection: %s", body)
	}
	// The collection is scanned just after the download is marked done.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		_, body = e.do(t, "GET", "/api/v1/catalog/movie?q=1408", nil, true)
		var page struct {
			Items []struct {
				SubscriptionID int64 `json:"subscription_id"`
			} `json:"items"`
		}
		json.Unmarshal(body, &page)
		if len(page.Items) == 1 && page.Items[0].SubscriptionID != 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("local copy first: %s", body)
		}
	}

	// A whole series: each episode in its season folder.
	series := findItem(t, e, "series", "twin")
	if code, body := e.do(t, "POST", "/api/v1/downloads", map[string]any{"media": "series", "id": series}, true); code != 201 || !strings.Contains(string(body), `"added":3`) {
		t.Fatalf("series: %d %s", code, body)
	}
	list := waitDownloads(t, e, 4)
	var eps []string
	for _, d := range list {
		if strings.HasPrefix(d.Path, "TV Shows/") {
			eps = append(eps, d.Path)
		}
	}
	if len(eps) != 3 || !strings.Contains(strings.Join(eps, "|"), "TV Shows/Twin Peaks/Season 2/") {
		t.Fatalf("episodes: %v", eps)
	}
	// The episode with a picture gets it next to the file.
	var pilot string
	for _, p := range eps {
		if strings.Contains(p, "S01E01") {
			pilot = filepath.Join(dir, filepath.FromSlash(strings.TrimSuffix(p, filepath.Ext(p))))
		}
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, err := os.Stat(pilot + "-thumb.png"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no picture next to %s", pilot)
		}
	}

	// Deleting removes the file and leaves the collection without it.
	id := strconv.FormatInt(list[0].ID, 10)
	if code, _ := e.do(t, "DELETE", "/api/v1/downloads/"+id, nil, true); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(list[0].Path))); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
	// A window that starts and ends at the same hour is refused.
	if code, _ := e.do(t, "PUT", "/api/v1/settings/downloads", map[string]any{"night_only": true, "from_hour": 3, "to_hour": 3}, true); code != 400 {
		t.Fatal("empty window accepted")
	}
}

// The downloads collection saved with another data folder (restored from a
// backup, folder moved): it is pointed at this server's folder instead of
// failing on its name, and the downloaded film shows up.
func TestDownloadsCollectionFromAnotherFolder(t *testing.T) {
	e, _ := tvSetup(t)
	dir := t.TempDir()
	old := filepath.Join(t.TempDir(), filepath.Base(dir), "Movies")
	os.MkdirAll(old, 0o755)
	if code, body := e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "Scaricati · Film", "kind": "local", "path": old, "content": "movie"}, true); code != 201 {
		t.Fatalf("old collection: %d %s", code, body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := e.svc.StartDownloads(ctx, dir, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	e.do(t, "POST", "/api/v1/downloads", map[string]any{"media": "movie", "id": findItem(t, e, "movie", "1408")}, true)
	waitDownloads(t, e, 1)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		_, body := e.do(t, "GET", "/api/v1/subscriptions", nil, true)
		var subs []struct {
			Name     string
			Settings struct{ Path string }
		}
		json.Unmarshal(body, &subs)
		n, here := 0, false
		for _, s := range subs {
			if strings.HasPrefix(s.Name, "Scaricati · Film") {
				n++
				here = here || s.Settings.Path == filepath.Join(dir, "Movies")
			}
		}
		if n == 1 && here {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("downloads collection: %s", body)
		}
	}
}
