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
