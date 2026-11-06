package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) (*Store, int64) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	vpnID, err := st.AddVPN(ctx, "test", "wireguard", func(int64) []byte { return []byte("sealed") })
	if err != nil {
		t.Fatal(err)
	}
	subID, err := st.AddSubscription(ctx, Subscription{Name: "B", Kind: "xtream", VPNID: vpnID, Settings: json.RawMessage(`{}`)},
		func(int64) []byte { return []byte("sealed") })
	if err != nil {
		t.Fatal(err)
	}
	return st, subID
}

func movies(n int, prefix string) []Item {
	out := make([]Item, n)
	for i := range out {
		title := fmt.Sprintf("%s %05d", prefix, i)
		out[i] = Item{Kind: "movie", SourceID: fmt.Sprint(i), Title: title, DisplayTitle: title, Year: 2000 + i%20}
	}
	return out
}

func TestReplaceKeepsIDsAndMarksRemovals(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	items := movies(100, "Film")
	res, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items, false)
	if err != nil || res.Added != 100 || res.Total["movie"] != 100 {
		t.Fatalf("first import: %+v %v", res, err)
	}
	first, _ := st.Catalog(ctx, Query{Kind: "movie", Limit: 1})

	// Second import: one title renamed, ten removed.
	items[0].DisplayTitle, items[0].Title = "Film 00000 rinominato", "Film 00000 rinominato"
	res, err = st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items[:90], false)
	if err != nil || res.Added != 0 || res.Removed != 10 {
		t.Fatalf("second import: %+v %v", res, err)
	}
	again, _ := st.Catalog(ctx, Query{Kind: "movie", Limit: 1})
	if again.Items[0].ID != first.Items[0].ID || again.Items[0].DisplayTitle != "Film 00000 rinominato" {
		t.Fatalf("id changed or update lost: %+v vs %+v", again.Items[0], first.Items[0])
	}
	counts, _ := st.Counts(ctx, sub)
	if counts["movie"] != 90 {
		t.Fatalf("available count %d", counts["movie"])
	}
}

func TestSuspiciousDropKeepsPreviousCatalog(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(200, "Film"), false); err != nil {
		t.Fatal(err)
	}
	_, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(50, "Film"), false)
	if !errors.Is(err, ErrSuspiciousDrop) {
		t.Fatalf("expected drop protection, got %v", err)
	}
	if counts, _ := st.Counts(ctx, sub); counts["movie"] != 200 {
		t.Fatalf("previous catalog not kept: %v", counts)
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(50, "Film"), true); err != nil {
		t.Fatalf("explicit allow_drop refused: %v", err)
	}
}

func TestPaginationAndSearch(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	items := movies(250, "Film")
	items = append(items,
		Item{Kind: "movie", SourceID: "x1", Title: "La città incantata", DisplayTitle: "La città incantata"},
		Item{Kind: "movie", SourceID: "x2", Title: "Città di Dio", DisplayTitle: "Città di Dio"},
	)
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items, false); err != nil {
		t.Fatal(err)
	}
	seen, cursor, pages := map[int64]bool{}, "", 0
	for {
		page, err := st.Catalog(ctx, Query{Kind: "movie", Limit: 60, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			if seen[it.ID] {
				t.Fatalf("duplicate %d across pages", it.ID)
			}
			seen[it.ID] = true
		}
		pages++
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(seen) != 252 || pages != 5 {
		t.Fatalf("paged %d items in %d pages", len(seen), pages)
	}

	// Accent-insensitive prefix search.
	res, err := st.Catalog(ctx, Query{Kind: "movie", Text: "citta inc"})
	if err != nil || len(res.Items) != 1 || res.Items[0].SourceID != "x1" {
		t.Fatalf("search: %+v %v", res.Items, err)
	}
	if res, _ := st.Catalog(ctx, Query{Kind: "movie", Text: "CITTÀ"}); len(res.Items) != 2 {
		t.Fatalf("search all: %d", len(res.Items))
	}
	// FTS syntax in user input must not break or widen the query.
	if _, err := st.Catalog(ctx, Query{Kind: "movie", Text: `") OR * NEAR(`}); err != nil {
		t.Fatalf("hostile search text: %v", err)
	}
}

func TestReadsDoNotWaitForImport(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(1000, "Film"), false); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(20000, "Nuovo"), false)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	for range 20 {
		if _, err := st.Catalog(ctx, Query{Kind: "movie", Limit: 50}); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("reads blocked during import: %v", d)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Migration 12 rebuilds the subscription table: the catalog that refers
// to it must survive, and a local collection needs no VPN.
func TestSubscriptionRebuildKeepsCatalog(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "vodmax.db")
	all := migrations
	migrations = all[:11]
	st, err := Open(path)
	if err != nil {
		migrations = all
		t.Fatal(err)
	}
	vpnID, _ := st.AddVPN(ctx, "test", "wireguard", func(int64) []byte { return []byte("sealed") })
	subID, err := st.AddSubscription(ctx, Subscription{Name: "B", Kind: "xtream", VPNID: vpnID, Settings: json.RawMessage(`{}`)},
		func(int64) []byte { return []byte("sealed") })
	if err == nil {
		_, err = st.ReplaceCatalog(ctx, subID, []string{"movie"}, nil, movies(10, "Film"), false)
	}
	st.Close()
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if c, err := st.Counts(ctx, subID); err != nil || c["movie"] != 10 {
		t.Fatalf("film dopo la migrazione: %v %v", c, err)
	}
	local, err := st.AddSubscription(ctx, Subscription{Name: "Raccolta", Kind: "local", Settings: json.RawMessage(`{}`)},
		func(int64) []byte { return []byte("sealed") })
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.Subscription(ctx, local)
	if err != nil || sub.VPNID != 0 {
		t.Fatalf("raccolta: %+v %v", sub, err)
	}
	// Foreign keys are back on: deleting the subscription removes its items.
	if err := st.DeleteSubscription(ctx, subID); err != nil {
		t.Fatal(err)
	}
	var n int
	st.r.QueryRowContext(ctx, `SELECT count(*) FROM item`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d film rimasti dopo la cancellazione dell'abbonamento", n)
	}
}

// Before a newer version migrates the schema, the database is copied as
// it was: the copy opens with the older schema and holds the same data.
func TestCopyBeforeMigrating(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "vodmax.db")
	all := migrations
	migrations = all[:16]
	st, err := Open(path)
	if err != nil {
		migrations = all
		t.Fatal(err)
	}
	vpnID, _ := st.AddVPN(ctx, "test", "wireguard", func(int64) []byte { return []byte("sealed") })
	st.Close()
	if copies, _ := filepath.Glob(filepath.Join(dir, "pre-upgrade", "*.db")); len(copies) != 0 {
		migrations = all
		t.Fatalf("copy of a new database: %v", copies)
	}
	migrations = all
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	copies, _ := filepath.Glob(filepath.Join(dir, "pre-upgrade", "vodmax-schema16-*.db"))
	if len(copies) != 1 {
		t.Fatalf("pre-upgrade copies: %v", copies)
	}
	// The older version opens the copy.
	migrations = all[:16]
	old, err := Open(copies[0])
	migrations = all
	if err != nil {
		t.Fatalf("older version on the copy: %v", err)
	}
	defer old.Close()
	if _, err := old.VPN(ctx, vpnID); err != nil {
		t.Fatalf("data in the copy: %v", err)
	}
	// Opening again without a newer schema makes no copy.
	st, _ = Open(path)
	st.Close()
	if copies, _ := filepath.Glob(filepath.Join(dir, "pre-upgrade", "*.db")); len(copies) != 1 {
		t.Fatalf("copies after a plain start: %v", copies)
	}
}

// The image cache keeps the newest images within age and size; the rest
// goes (it is fetched again when asked).
func TestPruneImageCache(t *testing.T) {
	st, _ := openTest(t)
	ctx := context.Background()
	now := time.Now()
	for i, age := range []time.Duration{0, time.Hour, 2 * time.Hour, 3 * time.Hour, 40 * 24 * time.Hour} {
		hash := fmt.Sprintf("h%d", i)
		if err := st.PutCachedImage(ctx, hash, CachedImage{ContentType: "image/png", Data: make([]byte, 100)}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.w.ExecContext(ctx, `UPDATE image_cache SET fetched_at = ? WHERE url_hash = ?`, now.Add(-age).Unix(), hash); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.PruneImageCache(ctx, now.Add(-30*24*time.Hour), 250)
	if err != nil || n != 3 {
		t.Fatalf("pruned %d, %v; want 3", n, err)
	}
	for i, want := range []bool{true, true, false, false, false} {
		if _, ok, _ := st.CachedImage(ctx, fmt.Sprintf("h%d", i)); ok != want {
			t.Errorf("h%d kept=%v, want %v", i, ok, want)
		}
	}
}

// A recording stopped early ends before the programme it was for, but
// still covers it: a rule must not record the programme again.
func TestRecordingCoversItsProgrammeAfterAnEarlyStop(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	id, err := s.AddRecording(ctx, Recording{ItemID: 1, GroupKey: "g", Channel: "C", Title: "P", Start: 1000, Stop: 5000})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Recording(ctx, id)
	r.Status, r.Stop = "completed", 1100 // stopped during the margin before the programme
	if err := s.UpdateRecording(ctx, r); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Recording(ctx, id); r.Stop != 1100 {
		t.Fatalf("real stop not saved: %d", r.Stop)
	}
	if got, _ := s.RecordingCovers(ctx, "g", 1180, 4640, 1000); got != "completed" {
		t.Fatalf("covers: %q", got)
	}
	if got, _ := s.RecordingCovers(ctx, "g", 5180, 8000, 5000); got != "" {
		t.Fatalf("the next programme: %q", got)
	}
}

// A backup made by an older version (no profiles, no recording files)
// still restores, leaving the profiles as they are. A download queued
// before the restore finds its film again after the catalog is back, not
// whatever title gets its old id.
func TestImportBackupFromAnOlderVersion(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	if _, err := st.AddProfile(ctx, Profile{Name: "Bimbi", Kind: "kids"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(3, "Film"), false); err != nil {
		t.Fatal(err)
	}
	filmID := func() (id int64) {
		st.r.QueryRowContext(ctx, `SELECT id FROM item WHERE kind = 'movie' AND title = 'Film 00002'`).Scan(&id)
		return id
	}
	if _, _, err := st.AddDownload(ctx, Download{Media: "movie", MediaID: filmID(), SubscriptionID: sub, SourceID: "2",
		Title: "Film 00002", Path: "Movies/Film/Film.mp4", Meta: "{}"}); err != nil {
		t.Fatal(err)
	}
	old := `{"version":1,"created_at":"2026-10-01T00:00:00Z","vpns":[{"id":1,"name":"test","config":"[Interface]"}],
		"subscriptions":[{"id":1,"name":"B","kind":"xtream","vpn_id":1,"settings":{},"rules":{},"secret":{}}],
		"recordings":[{"id":7,"channel":"Rai 1","title":"TG1","start":100,"stop":200,"status":"completed","bytes":10,"created_at":90}],
		"recording_groups":{"7":"g"},"settings":{"schedule":"{}"}}`
	var b Backup
	if err := json.Unmarshal([]byte(old), &b); err != nil {
		t.Fatal(err)
	}
	seal := func(int64, []byte) []byte { return []byte("sealed") }
	if err := st.ImportBackup(ctx, &b, seal, seal, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.Profiles(ctx); len(p) != 2 || p[1].Name != "Bimbi" || p[1].Kind != "kids" {
		t.Fatalf("profiles: %+v", p)
	}
	if r, err := st.Recording(ctx, 7); err != nil || r.Status != "failed" || r.File != "" {
		t.Fatalf("recording without its file: %+v %v", r, err)
	}
	// The catalog comes back in another order: the old id is another film.
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, append(movies(3, "Altro")[:2], movies(3, "Film")[2]), false); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyPendingRestore(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := st.Downloads(ctx)
	if len(list) != 1 || list[0].MediaID != filmID() {
		t.Fatalf("download points at %d, the film is %d", list[0].MediaID, filmID())
	}
}

// An empty guide (a busy panel's blank or HTML answer) does not wipe the
// programmes still to come; once they are over it is taken.
func TestEmptyGuideKeepsThePrevious(t *testing.T) {
	st, _ := openTest(t)
	ctx := context.Background()
	now := time.Now().Unix()
	chans := []GuideChannel{{ID: "c1", NameKey: "uno"}}
	progs := []GuideProgramme{{Channel: "c1", Start: now - 600, Stop: now + 600, Title: "In onda"},
		{Channel: "c1", Start: now + 600, Stop: now + 1800, Title: "Dopo"}}
	if err := st.ReplaceGuide(ctx, "sub:1", chans, progs); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceGuide(ctx, "sub:1", nil, nil); !errors.Is(err, ErrGuideEmpty) {
		t.Fatalf("empty guide taken: %v", err)
	}
	var n int
	st.r.QueryRowContext(ctx, `SELECT count(*) FROM epg_programme WHERE stop >= ?`, now).Scan(&n)
	if n != 2 {
		t.Fatalf("programmes left: %d", n)
	}
	// Only past programmes: an empty guide is accepted.
	st.w.ExecContext(ctx, `UPDATE epg_programme SET start = start - 7200, stop = stop - 7200`)
	if err := st.ReplaceGuide(ctx, "sub:1", nil, nil); err != nil {
		t.Fatalf("empty guide with nothing to come: %v", err)
	}
}
