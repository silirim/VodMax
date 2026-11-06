package service

import (
	"context"
	"testing"

	"github.com/silirim/vodmax/internal/store"
)

// Back to back on one channel: the second recording asks without priority
// (it would cut a viewer of another channel for minutes the first one is
// recording) until the first ends; the first keeps its priority.
func TestBackToBackSecondWaitsForTheFirst(t *testing.T) {
	s := &Service{}
	a := &activeRec{rec: store.Recording{ID: 1, GroupKey: "rai 1", Start: 100}}
	b := &activeRec{rec: store.Recording{ID: 2, GroupKey: "rai 1", Start: 200}}
	other := &activeRec{rec: store.Recording{ID: 3, GroupKey: "rai 2", Start: 50}}
	s.rec.running = map[int64]*activeRec{1: a, 2: b, 3: other}
	if !s.earlierRunning(b) {
		t.Fatal("the second recording asks with priority while the first runs")
	}
	if s.earlierRunning(a) || s.earlierRunning(other) {
		t.Fatal("the first recording lost its priority")
	}
	delete(s.rec.running, 1)
	if s.earlierRunning(b) {
		t.Fatal("the second recording got no priority back after the first ended")
	}
}

// Back to back, tuning the channel follows the recording that has data,
// not the next one still waiting with an empty file.
func TestTuneFollowsTheRecordingWithData(t *testing.T) {
	s, sub := oneConnectionService(t)
	ctx := context.Background()
	if _, err := s.st.ReplaceCatalog(ctx, sub, []string{"live"}, nil,
		[]store.Item{{Kind: "live", SourceID: "101", Title: "Rai 1", DisplayTitle: "Rai 1", GroupKey: "rai 1"}}, false); err != nil {
		t.Fatal(err)
	}
	ch, err := s.st.ChannelForGroup(ctx, "rai 1")
	if err != nil {
		t.Fatal(err)
	}
	a := &activeRec{rec: store.Recording{ID: 1, GroupKey: "rai 1", Start: 100}, bytes: 71064}
	b := &activeRec{rec: store.Recording{ID: 2, GroupKey: "rai 1", Start: 200}}
	s.rec.running = map[int64]*activeRec{1: a, 2: b}
	for range 20 { // map order is random
		if got := s.activeRecordingFor(ctx, ch.ID); got != a {
			t.Fatalf("followed recording %d with %d bytes", got.rec.ID, got.size())
		}
	}
}
