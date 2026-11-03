package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A recording in progress can be watched from any second recorded so far,
// or from close to its end; the stream always starts on a TS packet.
func TestRecordingOffsetAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.ts")
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	data := []byte{0, 0, 0} // out of step, as after a reconnection
	for len(data) < 188*10000 {
		data = append(data, pkt...)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	a := &activeRec{bytes: int64(len(data)), began: time.Now().Add(-100 * time.Second)}
	rate := float64(len(data)) / 100
	for _, c := range []struct {
		second float64
		want   float64 // bytes, about
	}{{10, rate * 10}, {50, rate * 50}, {-1, float64(len(data)) - rate*liveEdge}, {99, float64(len(data)) - rate*liveEdge}, {1e9, float64(len(data)) - rate*liveEdge}} {
		got := a.offsetAt(f, c.second)
		if (got-3)%188 != 0 || data[got] != 0x47 {
			t.Fatalf("second %v: offset %d is not on a packet", c.second, got)
		}
		if d := float64(got) - c.want; d < -188*2 || d > 188*2 {
			t.Fatalf("second %v: offset %d, want about %.0f", c.second, got, c.want)
		}
	}
	if got := (&activeRec{}).offsetAt(f, 30); got != 0 {
		t.Fatalf("nothing recorded yet: %d", got)
	}
}
