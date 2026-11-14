package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
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

// selectAll stands in for the selection that follows an import: new rows
// start hidden and not primary.
func selectAll(st *Store) {
	st.w.Exec(`UPDATE item SET visible = 1, primary_variant = 1`)
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
	res, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items, false, nil)
	if err != nil || res.Added != 100 || res.Total["movie"] != 100 {
		t.Fatalf("first import: %+v %v", res, err)
	}
	selectAll(st)
	first, _ := st.Catalog(ctx, Query{Kind: "movie", Limit: 1})

	// Second import: one title renamed, ten removed.
	items[0].DisplayTitle, items[0].Title = "Film 00000 rinominato", "Film 00000 rinominato"
	res, err = st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items[:90], false, nil)
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
	// Third import: the ten come back; titles that did not change are
	// not written again.
	var seenBefore int64
	st.r.QueryRowContext(ctx, `SELECT last_seen_at FROM item WHERE source_id = '50'`).Scan(&seenBefore)
	res, err = st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items, false, nil)
	if err != nil || res.Added != 0 || res.Removed != 0 {
		t.Fatalf("third import: %+v %v", res, err)
	}
	var seenAfter int64
	var available int
	st.r.QueryRowContext(ctx, `SELECT last_seen_at FROM item WHERE source_id = '50'`).Scan(&seenAfter)
	st.r.QueryRowContext(ctx, `SELECT count(*) FROM item WHERE available = 1`).Scan(&available)
	if seenAfter != seenBefore || available != 100 {
		t.Fatalf("unchanged row rewritten (%d → %d) or removed ones not back: %d", seenBefore, seenAfter, available)
	}
}

// A large import tells how far it is, and gives the same results as the
// upsert it replaced: ids kept, unchanged rows not written (no search
// index rebuild), a title that goes and comes back on its row, a title
// listed twice and changed counted twice, the search on the new names.
func TestReplaceProgressAndSameResults(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	items := movies(12000, "Film")
	var calls [][2]int
	progress := func(done, total int) { calls = append(calls, [2]int{done, total}) }
	res, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items, false, progress)
	if err != nil || res.Added != 12000 || res.Removed != 0 {
		t.Fatalf("first import: %+v %v", res, err)
	}
	if want := [][2]int{{0, 12000}, {5000, 12000}, {10000, 12000}, {12000, 12000}}; !slices.Equal(calls, want) {
		t.Fatalf("progress %v, want %v", calls, want)
	}
	selectAll(st)
	ids, _ := st.ItemIDs(ctx, sub, "movie")
	var seen int64
	st.r.QueryRowContext(ctx, `SELECT last_seen_at FROM item WHERE source_id = '7'`).Scan(&seen)

	calls = nil
	res, err = st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items, false, progress)
	if err != nil || res.Added != 0 || res.Removed != 0 || slices.Contains(calls, [2]int{12000, 12000}) {
		t.Fatalf("same catalog: %+v %v, progress %v", res, err, calls)
	}
	var again int64
	st.r.QueryRowContext(ctx, `SELECT last_seen_at FROM item WHERE source_id = '7'`).Scan(&again)
	if now, _ := st.ItemIDs(ctx, sub, "movie"); !maps.Equal(now, ids) || again != seen {
		t.Fatalf("same catalog moved ids or rewrote a row (%d → %d)", seen, again)
	}

	// One renamed, one gone, one new and listed twice with a change.
	changed := slices.Clone(items[1:])
	changed[6].Title, changed[6].DisplayTitle = "Pellicola rara", "Pellicola rara"
	changed = append(changed, Item{Kind: "movie", SourceID: "nuovo", Title: "Nuovo", DisplayTitle: "Nuovo"},
		Item{Kind: "movie", SourceID: "nuovo", Title: "Nuovo", DisplayTitle: "Nuovo", Year: 2024})
	res, err = st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, changed, false, nil)
	if err != nil || res.Added != 2 || res.Removed != 1 {
		t.Fatalf("changed catalog: %+v %v", res, err)
	}
	selectAll(st)
	if p, _ := st.Catalog(ctx, Query{Kind: "movie", Text: "rara"}); len(p.Items) != 1 || p.Items[0].ID != ids["7"] {
		t.Fatalf("renamed title: %+v", p.Items)
	}
	if p, _ := st.Catalog(ctx, Query{Kind: "movie", Text: "Film 00007"}); len(p.Items) != 0 {
		t.Fatalf("old name still found: %+v", p.Items)
	}
	var available int
	st.r.QueryRowContext(ctx, `SELECT available FROM item WHERE id = ?`, ids["0"]).Scan(&available)
	if available != 0 {
		t.Fatal("gone title still available")
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items, false, nil); err != nil {
		t.Fatal(err)
	}
	now, _ := st.ItemIDs(ctx, sub, "movie")
	st.r.QueryRowContext(ctx, `SELECT available FROM item WHERE id = ?`, ids["0"]).Scan(&available)
	if now["0"] != ids["0"] || now["7"] != ids["7"] || available != 1 {
		t.Fatalf("title back on another row (%d → %d) or not available", ids["0"], now["0"])
	}
}

func TestSuspiciousDropKeepsPreviousCatalog(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(200, "Film"), false, nil); err != nil {
		t.Fatal(err)
	}
	selectAll(st)
	_, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(50, "Film"), false, nil)
	if !errors.Is(err, ErrSuspiciousDrop) {
		t.Fatalf("expected drop protection, got %v", err)
	}
	if counts, _ := st.Counts(ctx, sub); counts["movie"] != 200 {
		t.Fatalf("previous catalog not kept: %v", counts)
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(50, "Film"), true, nil); err != nil {
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
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items, false, nil); err != nil {
		t.Fatal(err)
	}
	selectAll(st)
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
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(1000, "Film"), false, nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(20000, "Nuovo"), false, nil)
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
		_, err = st.ReplaceCatalog(ctx, subID, []string{"movie"}, nil, movies(10, "Film"), false, nil)
		selectAll(st)
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
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(3, "Film"), false, nil); err != nil {
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
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, append(movies(3, "Altro")[:2], movies(3, "Film")[2]), false, nil); err != nil {
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

// A copy made while a recording was running, restored on the same server
// once it has finished: the recording stays completed, with its file (the
// MP4 of the conversion, or the TS) and what it has now, not "interrupted"
// and not twice.
func TestRestoreKeepsARecordingFinishedSinceTheCopy(t *testing.T) {
	for _, ext := range []string{".mp4", ".ts"} {
		st, _ := openTest(t)
		ctx := context.Background()
		id, err := st.AddRecording(ctx, Recording{GroupKey: "g", Channel: "Rai 1", Title: "Film", Start: 100, Stop: 200})
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(t.TempDir(), "1-film")
		st.UpdateRecording(ctx, Recording{ID: id, Status: "recording", File: base + ".ts", Bytes: 10, Stop: 200})
		open := func(int64, []byte) ([]byte, error) { return []byte("{}"), nil }
		b, err := st.ExportBackup(ctx, open, open)
		if err != nil {
			t.Fatal(err)
		}
		file := base + ext // finished early, then converted or left in MPEG-TS
		st.UpdateRecording(ctx, Recording{ID: id, Status: "completed", File: file, Bytes: 500, Stop: 190})
		seal := func(int64, []byte) []byte { return []byte("sealed") }
		recFile := func(name string) string {
			if name == filepath.Base(file) {
				return file
			}
			return ""
		}
		if err := st.ImportBackup(ctx, b, seal, seal, recFile); err != nil {
			t.Fatal(err)
		}
		l, _ := st.Recordings(ctx)
		if len(l) != 1 || l[0].ID != id || l[0].Status != "completed" || l[0].File != file || l[0].Bytes != 500 || l[0].Stop != 190 || l[0].Error != "" {
			t.Fatalf("%s after the restore: %+v", ext, l)
		}
	}
}

// A copy made after a recording finished in MPEG-TS and before its
// conversion (at the next start, once ffmpeg or the disk space is there),
// restored on the same server once the MP4 replaced the TS: one row, same
// number, pointing to the MP4.
func TestRestoreKeepsARecordingConvertedSinceTheCopy(t *testing.T) {
	st, _ := openTest(t)
	ctx := context.Background()
	id, err := st.AddRecording(ctx, Recording{GroupKey: "g", Channel: "Rai 1", Title: "Film", Start: 100, Stop: 200})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st.UpdateRecording(ctx, Recording{ID: id, Status: "completed", File: filepath.Join(dir, "1-film.ts"), Bytes: 600, Stop: 200})
	open := func(int64, []byte) ([]byte, error) { return []byte("{}"), nil }
	b, err := st.ExportBackup(ctx, open, open)
	if err != nil {
		t.Fatal(err)
	}
	mp4 := filepath.Join(dir, "1-film.mp4")
	st.UpdateRecording(ctx, Recording{ID: id, Status: "completed", File: mp4, Bytes: 500, Stop: 200})
	seal := func(int64, []byte) []byte { return []byte("sealed") }
	recFile := func(name string) string {
		if name == "1-film.mp4" {
			return mp4
		}
		return ""
	}
	if err := st.ImportBackup(ctx, b, seal, seal, recFile); err != nil {
		t.Fatal(err)
	}
	l, _ := st.Recordings(ctx)
	if len(l) != 1 || l[0].ID != id || l[0].Status != "completed" || l[0].File != mp4 || l[0].Bytes != 500 || l[0].Error != "" {
		t.Fatalf("after the restore: %+v", l)
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
	// A stale file with only yesterday's programmes is no better.
	stale := []GuideProgramme{{Channel: "c1", Start: now - 90000, Stop: now - 86400, Title: "Ieri"}}
	if err := st.ReplaceGuide(ctx, "sub:1", chans, stale); !errors.Is(err, ErrGuideEmpty) {
		t.Fatalf("stale guide taken: %v", err)
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

// Favourites, hidden titles, recordings and rule channels saved under the
// group keys of older versions (non-Latin letters dropped: "Первый канал"
// and "الجزيرة" were both "") follow their titles at the next import.
func TestGroupKeysCarriedOverAfterUpgrade(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	items := func(keys ...string) []Item {
		titles := []string{"Первый канал", "الجزيرة", "Россия 1", "Беларусь 1", "Rai 1"}
		out := []Item{}
		for i, title := range titles {
			out = append(out, Item{Kind: "live", SourceID: fmt.Sprint(i + 1), Title: title, DisplayTitle: title, GroupKey: keys[i]})
		}
		return append(out, Item{Kind: "movie", SourceID: "10", Title: "Москва слезам не верит", DisplayTitle: "Москва слезам не верит",
			Year: 1980, GroupKey: keys[len(titles)]})
	}
	// The database as an older version left it.
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"live", "movie"}, nil, items("", "", "1", "1", "rai 1", "|1980"), false, nil); err != nil {
		t.Fatal(err)
	}
	id := func(src string) int64 {
		var id int64
		st.r.QueryRowContext(ctx, `SELECT id FROM item WHERE source_id = ?`, src).Scan(&id)
		return id
	}
	for _, src := range []string{"1", "5", "10"} {
		if err := st.SetFavorite(ctx, 7, id(src), true); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.HideContinue(ctx, 7, id("10")); err != nil {
		t.Fatal(err)
	}
	ruleID, _ := st.AddRecordingRule(ctx, RecordingRule{Name: "Notizie", Spec: RuleSpec{All: []string{"новости"}, Channel: "1"}, Enabled: true})
	anyID, _ := st.AddRecordingRule(ctx, RecordingRule{Name: "Ovunque", Spec: RuleSpec{All: []string{"x"}}, Enabled: true})
	if _, err := st.AddRecording(ctx, Recording{ItemID: id("3"), GroupKey: "1", Title: "Новости", Start: 1, Stop: 2, RuleID: ruleID}); err != nil {
		t.Fatal(err)
	}

	// The next import computes the new keys.
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"live", "movie"}, nil,
		items("первый канал", "الجزيرة", "россия 1", "беларусь 1", "rai 1", "москва слезам не верит|1980"), false, nil); err != nil {
		t.Fatal(err)
	}
	favs := map[string]bool{}
	rows, _ := st.r.QueryContext(ctx, `SELECT kind || ':' || group_key FROM favorite WHERE profile_id = 7`)
	for rows.Next() {
		var k string
		rows.Scan(&k)
		favs[k] = true
	}
	rows.Close()
	// "" starred both channels before: both stay starred.
	want := []string{"live:первый канал", "live:الجزيرة", "live:rai 1", "movie:москва слезам не верит|1980"}
	if len(favs) != len(want) {
		t.Fatalf("favourites: %v", favs)
	}
	for _, k := range want {
		if !favs[k] {
			t.Fatalf("favourite %q missing: %v", k, favs)
		}
	}
	var hidden, recKey string
	st.r.QueryRowContext(ctx, `SELECT title_key FROM continue_hidden WHERE profile_id = 7`).Scan(&hidden)
	st.r.QueryRowContext(ctx, `SELECT group_key FROM recording`).Scan(&recKey)
	if hidden != "movie:москва слезам не верит|1980" || recKey != "россия 1" {
		t.Fatalf("hidden %q, recording %q", hidden, recKey)
	}
	rule, _ := st.RecordingRule(ctx, ruleID)
	anyRule, _ := st.RecordingRule(ctx, anyID)
	if rule.Spec.Channel != "россия 1" || anyRule.Spec.Channel != "" || rule.Spec.All[0] != "новости" {
		t.Fatalf("rules: %+v %+v", rule.Spec, anyRule.Spec)
	}
	if _, err := st.ChannelForGroup(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("channel for the empty key: %v", err)
	}
}

// A new title stays hidden until the selection decides about it: a title
// of an adult category is never open to kids in between. Titles already
// shown keep their state on the next import.
func TestNewTitlesHiddenUntilSelection(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(3, "Film"), false, nil); err != nil {
		t.Fatal(err)
	}
	if page, _ := st.Catalog(ctx, Query{Kind: "movie", NoAdult: true}); len(page.Items) != 0 {
		t.Fatalf("new titles shown before the selection: %d", len(page.Items))
	}
	selectAll(st)
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(4, "Film"), false, nil); err != nil {
		t.Fatal(err)
	}
	if page, _ := st.Catalog(ctx, Query{Kind: "movie", NoAdult: true}); len(page.Items) != 3 {
		t.Fatalf("titles already shown: %d", len(page.Items))
	}
}

// Two providers can use the same category id for different categories:
// each is its own entry and lists only its own titles.
func TestCategoriesOfTwoSubscriptions(t *testing.T) {
	st, a := openTest(t)
	ctx := context.Background()
	vpn, _ := st.AddVPN(ctx, "altra", "wireguard", func(int64) []byte { return []byte("sealed") })
	b, err := st.AddSubscription(ctx, Subscription{Name: "C", Kind: "xtream", VPNID: vpn, Settings: json.RawMessage(`{}`)},
		func(int64) []byte { return []byte("sealed") })
	if err != nil {
		t.Fatal(err)
	}
	for sub, c := range map[int64][2]string{a: {"Azione", "Heat"}, b: {"Horror", "Saw"}} {
		cats := []Category{{Kind: "movie", SourceID: "5", Name: c[0]}}
		items := []Item{{Kind: "movie", SourceID: "1", Title: c[1], DisplayTitle: c[1], CategoryID: "5", GroupKey: c[1]}}
		if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, cats, items, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	selectAll(st)
	browse, err := st.BrowseCategories(ctx, "movie", false, false)
	if err != nil || len(browse) != 2 || browse[0].Name != "Azione" || browse[0].Subscription != a || browse[1].Subscription != b {
		t.Fatalf("browse: %+v %v", browse, err)
	}
	page, err := st.Catalog(ctx, Query{Kind: "movie", CategoryID: "5", CategorySubscription: b})
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "Saw" {
		t.Fatalf("category of the second provider: %+v %v", page.Items, err)
	}
	if page, _ := st.Catalog(ctx, Query{Kind: "movie", CategoryID: "5"}); len(page.Items) != 2 {
		t.Fatalf("without the subscription (older clients): %d", len(page.Items))
	}
}

// Folders follow the provider's order of the categories, also after the
// provider reorders them; without it, and for rows from before the
// order was stored, categories go by name.
func TestCategoriesInProviderOrder(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	sync := func(names ...string) {
		t.Helper()
		var cats []Category
		var items []Item
		for _, n := range names {
			cats = append(cats, Category{Kind: "movie", SourceID: n, Name: n})
			items = append(items, Item{Kind: "movie", SourceID: n, Title: "Film " + n, DisplayTitle: "Film " + n, CategoryID: n, GroupKey: n})
		}
		// A live category first: positions count within a kind.
		cats = append([]Category{{Kind: "live", SourceID: "9", Name: "Canali"}}, cats...)
		if _, err := st.ReplaceCatalog(ctx, sub, []string{"live", "movie"}, cats, items, false, nil); err != nil {
			t.Fatal(err)
		}
		selectAll(st)
	}
	order := func(provider bool) string {
		t.Helper()
		list, err := st.BrowseCategories(ctx, "movie", false, provider)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range list {
			out = append(out, c.Name)
		}
		return strings.Join(out, ",")
	}
	sync("Películas 2026", "Azione", "Películas HBO")
	if got := order(true); got != "Películas 2026,Azione,Películas HBO" {
		t.Fatalf("provider order: %s", got)
	}
	if got := order(false); got != "Azione,Películas 2026,Películas HBO" {
		t.Fatalf("by name: %s", got)
	}
	var first int
	st.r.QueryRowContext(ctx, `SELECT position FROM category WHERE kind = 'movie' AND source_id = 'Películas 2026'`).Scan(&first)
	if first != 0 {
		t.Fatalf("position within the kind: %d", first)
	}
	sync("Películas HBO", "Películas 2026", "Azione")
	if got := order(true); got != "Películas HBO,Películas 2026,Azione" {
		t.Fatalf("after the provider reordered: %s", got)
	}
	st.w.ExecContext(ctx, `UPDATE category SET position = 0`)
	if got := order(true); got != "Azione,Películas 2026,Películas HBO" {
		t.Fatalf("rows without a position: %s", got)
	}
}

// A channel is in every folder one of its shown variants is in, and two
// subscriptions' folders stay apart even with the same id; a channel
// without a group key is only in its own row's folder.
func TestChannelFolders(t *testing.T) {
	st, a := openTest(t)
	ctx := context.Background()
	vpn, _ := st.AddVPN(ctx, "altra", "wireguard", func(int64) []byte { return []byte("sealed") })
	b, err := st.AddSubscription(ctx, Subscription{Name: "C", Kind: "xtream", VPNID: vpn, Settings: json.RawMessage(`{}`)},
		func(int64) []byte { return []byte("sealed") })
	if err != nil {
		t.Fatal(err)
	}
	live := func(sub int64, cats []Category, items []Item) {
		t.Helper()
		if _, err := st.ReplaceCatalog(ctx, sub, []string{"live"}, cats, items, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	// An M3U group name is the id, ':' included.
	live(a, []Category{{Kind: "live", SourceID: "1", Name: "Zeta"}, {Kind: "live", SourceID: "a:b", Name: "Azione"}}, []Item{
		{Kind: "live", SourceID: "1", Title: "Rai 1 HD", DisplayTitle: "Rai 1", CategoryID: "1", GroupKey: "rai1"},
		{Kind: "live", SourceID: "2", Title: "Senza 1", DisplayTitle: "Senza 1", CategoryID: "a:b"},
		{Kind: "live", SourceID: "3", Title: "Senza 2", DisplayTitle: "Senza 2", CategoryID: "1"},
	})
	live(b, []Category{{Kind: "live", SourceID: "1", Name: "Beta"}}, []Item{
		{Kind: "live", SourceID: "1", Title: "Rai 1 FHD", DisplayTitle: "Rai 1", CategoryID: "1", GroupKey: "rai1"},
	})
	selectAll(st)
	list, err := st.TVChannels(ctx, 0, false, "", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AttachFolders(ctx, list, false); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range list {
		got[c.Name] = strings.Join(c.Folders, ",")
	}
	ka, kb := fmt.Sprint(a)+":", fmt.Sprint(b)+":"
	want := map[string]string{"Rai 1": ka + "1," + kb + "1", "Senza 1": ka + "a:b", "Senza 2": ka + "1"}
	if !maps.Equal(got, want) {
		t.Fatalf("folders: %v, want %v", got, want)
	}
	// Subscription by subscription, each in its provider's order.
	cats, err := st.BrowseCategories(ctx, "live", false, true)
	if err != nil || len(cats) != 3 || cats[0].Name != "Zeta" || cats[1].Name != "Azione" || cats[2].Name != "Beta" || cats[2].Subscription != b {
		t.Fatalf("provider order of two subscriptions: %+v %v", cats, err)
	}
}

// Titles gone from the provider for long are deleted, unless something
// still points to them; a title gone recently stays.
func TestPurgeGoneItems(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	items := movies(7, "Film")
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items, false, nil); err != nil {
		t.Fatal(err)
	}
	id := func(src string) int64 {
		var id int64
		st.r.QueryRowContext(ctx, `SELECT id FROM item WHERE source_id = ?`, src).Scan(&id)
		return id
	}
	if _, err := st.SaveProgress(ctx, 1, "movie", id("1"), 60, 6000); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRecording(ctx, Recording{ItemID: id("2"), GroupKey: "x", Title: "R", Start: 1, Stop: 2}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AddDownload(ctx, Download{Media: "movie", MediaID: id("3"), SubscriptionID: sub, SourceID: "3", Title: "D", Path: "d.mkv"}); err != nil {
		t.Fatal(err)
	}
	st.SetItemHidden(ctx, id("4"), true)
	plain := id("5")
	st.PutMeta(ctx, fmt.Sprintf("p:%d", plain), "{}")
	// Only "6" is still offered; everything else went, "0" just now.
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, items[6:], true, nil); err != nil {
		t.Fatal(err)
	}
	month := time.Now().Add(-31 * 24 * time.Hour).UnixNano()
	st.w.ExecContext(ctx, `UPDATE item SET last_seen_at = ? WHERE source_id <> '0'`, month)
	n, err := st.PurgeGoneItems(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("purged %d: %v", n, err)
	}
	var left int
	var meta int
	st.r.QueryRowContext(ctx, `SELECT count(*) FROM item`).Scan(&left)
	st.r.QueryRowContext(ctx, `SELECT count(*) FROM meta_cache WHERE key = ?`, fmt.Sprintf("p:%d", plain)).Scan(&meta)
	if left != 6 || id("5") != 0 || meta != 0 {
		t.Fatalf("left %d rows, plain %d, meta %d", left, id("5"), meta)
	}
}

// A provider swaps the stream ids of two channels: a recording scheduled
// on Rai 1 stays on Rai 1, now under the other id.
func TestSwappedStreamIDsKeepRecordingChannel(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	live := func(a, b string) []Item {
		key := map[string]string{"Rai 1": "rai 1", "Rai 2": "rai 2"}
		return []Item{
			{Kind: "live", SourceID: "101", Title: a, DisplayTitle: a, GroupKey: key[a]},
			{Kind: "live", SourceID: "102", Title: b, DisplayTitle: b, GroupKey: key[b]},
		}
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"live"}, nil, live("Rai 1", "Rai 2"), false, nil); err != nil {
		t.Fatal(err)
	}
	var id int64
	st.r.QueryRowContext(ctx, `SELECT id FROM item WHERE source_id = '101'`).Scan(&id)
	now := time.Now().Unix()
	if _, err := st.AddRecording(ctx, Recording{ItemID: id, GroupKey: "rai 1", Channel: "Rai 1", Title: "Film", Start: now + 3600, Stop: now + 7200}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"live"}, nil, live("Rai 2", "Rai 1"), false, nil); err != nil {
		t.Fatal(err)
	}
	var key string
	st.r.QueryRowContext(ctx, `SELECT group_key FROM recording`).Scan(&key)
	ch, err := st.ChannelForGroup(ctx, key)
	if key != "rai 1" || err != nil || ch.Title != "Rai 1" {
		t.Fatalf("recording on Rai 1 now records %q (key %q, %v)", ch.Title, key, err)
	}
}

// A title without a key (non-Latin, no year, before 0.6.7) was taken off
// "continue watching" by its id: once it has a key it stays off.
func TestHiddenByIDStaysHiddenWithItsNewKey(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	film := func(key string) []Item {
		return []Item{{Kind: "movie", SourceID: "1", Title: "Солярис", DisplayTitle: "Солярис", GroupKey: key}}
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, film(""), false, nil); err != nil {
		t.Fatal(err)
	}
	selectAll(st)
	var id int64
	st.r.QueryRowContext(ctx, `SELECT id FROM item WHERE source_id = '1'`).Scan(&id)
	if _, err := st.SaveProgress(ctx, 1, "movie", id, 600, 6000); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // hidden after the last progress
	if err := st.HideContinue(ctx, 1, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, film("солярис"), false, nil); err != nil {
		t.Fatal(err)
	}
	var key string
	st.r.QueryRowContext(ctx, `SELECT title_key FROM continue_hidden`).Scan(&key)
	if l, err := st.Continue(ctx, 1, false, 10); err != nil || len(l) != 0 || key != "movie:солярис" {
		t.Fatalf("back in continue watching: %+v %v (hidden %q)", l, err, key)
	}
}

// A backup made before 0.6.7 brings favourites and rules on the old keys
// ("|1980", "1") into a database that never held them: the import that
// fills the catalog again carries them to the new keys.
func TestOldKeysOfARestoredBackupCarried(t *testing.T) {
	st, sub := openTest(t)
	ctx := context.Background()
	st.w.ExecContext(ctx, `INSERT INTO favorite (profile_id, kind, group_key, position, added_at) VALUES (7, 'movie', '|1980', 0, 1), (7, 'live', '1', 1, 1)`)
	ruleID, _ := st.AddRecordingRule(ctx, RecordingRule{Name: "Notizie", Spec: RuleSpec{All: []string{"новости"}, Channel: "1"}, Enabled: true})
	items := []Item{
		{Kind: "live", SourceID: "3", Title: "Россия 1", DisplayTitle: "Россия 1", GroupKey: "россия 1", LegacyKey: "1"},
		{Kind: "live", SourceID: "5", Title: "Rai 1", DisplayTitle: "Rai 1", GroupKey: "rai 1", LegacyKey: "rai 1"},
		{Kind: "movie", SourceID: "10", Title: "Москва слезам не верит", DisplayTitle: "Москва слезам не верит", Year: 1980,
			GroupKey: "москва слезам не верит|1980", LegacyKey: "|1980"},
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"live", "movie"}, nil, items, false, nil); err != nil {
		t.Fatal(err)
	}
	favs := map[string]bool{}
	rows, _ := st.r.QueryContext(ctx, `SELECT kind || ':' || group_key FROM favorite WHERE profile_id = 7`)
	for rows.Next() {
		var k string
		rows.Scan(&k)
		favs[k] = true
	}
	rows.Close()
	rule, _ := st.RecordingRule(ctx, ruleID)
	if len(favs) != 2 || !favs["movie:москва слезам не верит|1980"] || !favs["live:россия 1"] || rule.Spec.Channel != "россия 1" {
		t.Fatalf("favourites %v, rule channel %q", favs, rule.Spec.Channel)
	}
}
