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

// selectAll stands in for the selection that follows an import: new rows
// start hidden.
func selectAll(st *Store) {
	st.w.Exec(`UPDATE item SET visible = 1`)
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
	selectAll(st)
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
	selectAll(st)
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
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"live", "movie"}, nil, items("", "", "1", "1", "rai 1", "|1980"), false); err != nil {
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
		items("первый канал", "الجزيرة", "россия 1", "беларусь 1", "rai 1", "москва слезам не верит|1980"), false); err != nil {
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
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(3, "Film"), false); err != nil {
		t.Fatal(err)
	}
	if page, _ := st.Catalog(ctx, Query{Kind: "movie", NoAdult: true}); len(page.Items) != 0 {
		t.Fatalf("new titles shown before the selection: %d", len(page.Items))
	}
	selectAll(st)
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, nil, movies(4, "Film"), false); err != nil {
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
		if _, err := st.ReplaceCatalog(ctx, sub, []string{"movie"}, cats, items, false); err != nil {
			t.Fatal(err)
		}
	}
	selectAll(st)
	browse, err := st.BrowseCategories(ctx, "movie", false)
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
