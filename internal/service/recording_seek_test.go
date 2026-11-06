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

// Only whole packets reach the file: junk before the first one is
// skipped, a packet cut by the end of a read waits for the next, and one
// cut by the end of the connection is dropped.
func TestTSPacketsWholeAndInStep(t *testing.T) {
	packet := func(n byte) []byte {
		p := make([]byte, 188)
		p[0], p[1], p[2] = 0x47, n, 0x47 // a 0x47 inside the packet too
		return p
	}
	var stream []byte
	stream = append(stream, 0x47, 1, 2, 0x47) // junk, with sync bytes in it
	for n := byte(1); n <= 6; n++ {
		stream = append(stream, packet(n)...)
	}
	stream = append(stream, packet(7)[:100]...) // the connection ends here
	var out []byte
	var k tsPackets
	for rest := stream; len(rest) > 0; {
		n := min(len(rest), 77) // reads that end inside packets
		out = append(out, k.take(rest[:n])...)
		rest = rest[n:]
	}
	// The next connection starts in the middle of a packet.
	k = tsPackets{}
	next := append(packet(9)[150:], append(packet(10), packet(11)...)...)
	out = append(out, k.take(next)...)
	if len(out)%188 != 0 {
		t.Fatalf("%d bytes: not whole packets", len(out))
	}
	var got []byte
	for i := 0; i < len(out); i += 188 {
		if out[i] != 0x47 {
			t.Fatalf("packet at %d out of step", i)
		}
		got = append(got, out[i+1])
	}
	// Packet 7 went with the first connection; the second is in step again
	// from its first whole packet.
	if want := []byte{1, 2, 3, 4, 5, 6, 10, 11}; string(got) != string(want) {
		t.Fatalf("packets %v, want %v", got, want)
	}
}
