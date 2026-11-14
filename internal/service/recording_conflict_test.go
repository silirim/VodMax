package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
)

// conflictService has one subscription with two connections and three
// channels, "c1", "c2", "c3".
func conflictService(t *testing.T) (*Service, int64, map[string]int64) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, box, nil)
	seal := func(int64) []byte { return []byte("x") }
	vpnID, err := st.AddVPN(ctx, "v", "system", seal)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.AddSubscription(ctx, store.Subscription{Name: "B", Kind: "xtream", VPNID: vpnID, Settings: json.RawMessage(`{}`)}, seal)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSyncResult(ctx, sub, "ok", "", json.RawMessage(`{"max_connections":2}`)); err != nil {
		t.Fatal(err)
	}
	var items []store.Item
	for i, g := range []string{"c1", "c2", "c3"} {
		items = append(items, store.Item{Kind: "live", SourceID: string(rune('1' + i)), Title: g, DisplayTitle: g, GroupKey: g})
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"live"}, nil, items, false, nil); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, g := range []string{"c1", "c2", "c3"} {
		it, err := st.ChannelForGroup(ctx, g)
		if err != nil {
			t.Fatal(err)
		}
		ids[g] = it.ID
	}
	return s, sub, ids
}

// Two recordings one after the other on other channels never hold more
// than one connection at a time: with two connections a third recording
// spanning both fits.
func TestConflictCountsSimultaneousOnly(t *testing.T) {
	ctx := context.Background()
	s, sub, ids := conflictService(t)
	const base = 1_800_000_000
	for _, r := range []store.Recording{
		{ItemID: ids["c2"], GroupKey: "c2", Channel: "c2", Title: "A", Start: base, Stop: base + 3600},
		{ItemID: ids["c3"], GroupKey: "c3", Channel: "c3", Title: "B", Start: base + 5400, Stop: base + 9000},
	} {
		if _, err := s.st.AddRecording(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	why, err := s.recordingConflict(ctx, sub, "c1", base, base+9000)
	if err != nil {
		t.Fatal(err)
	}
	if why != "" {
		t.Fatalf("refused although at most two recordings run at once: %s", why)
	}
}

// Overlapping recordings still fill the connections.
func TestConflictStillRefusesWhenFull(t *testing.T) {
	ctx := context.Background()
	s, sub, ids := conflictService(t)
	const base = 1_800_000_000
	for _, r := range []store.Recording{
		{ItemID: ids["c2"], GroupKey: "c2", Channel: "c2", Title: "A", Start: base, Stop: base + 3600},
		{ItemID: ids["c3"], GroupKey: "c3", Channel: "c3", Title: "B", Start: base + 1800, Stop: base + 9000},
	} {
		if _, err := s.st.AddRecording(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// Both run between base+1800 and base+3600.
	why, err := s.recordingConflict(ctx, sub, "c1", base+3000, base+4000)
	if err != nil || why == "" {
		t.Fatalf("not refused with both connections taken: %q %v", why, err)
	}
	// After A ends one connection is free again.
	if why, _ := s.recordingConflict(ctx, sub, "c1", base+3600, base+7200); why != "" {
		t.Fatalf("refused with one connection free: %s", why)
	}
	// A recording of the same channel never counts against it.
	if why, _ := s.recordingConflict(ctx, sub, "c2", base+3000, base+4000); why != "" {
		t.Fatalf("own channel counted: %s", why)
	}
}
