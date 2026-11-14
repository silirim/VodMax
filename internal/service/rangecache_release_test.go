package service

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

// When the fetcher gives the provider connection back after an idle pause, a
// request that arrives while it is leaving must still be served: something
// has to pick it up. Only what the product itself uses is used here (the
// constructor, WriteRange, Close) and no field of the cache, so a fix is free
// to rename or reshape its insides. It waits for the idle release: about 25 s.
func TestRangeCacheRequestDuringIdleRelease(t *testing.T) {
	// larger than what one reader at the start makes the fetcher read ahead,
	// so the part asked for late is not on disk yet
	file := make([]byte, 40<<20)
	for i := range file {
		file[i] = byte(i*7 + i>>20)
	}
	const from, to = int64(38 << 20), int64(38<<20 + 3)
	var c *rangeCache
	var once sync.Once
	late := make(chan error, 1)
	var got bytes.Buffer
	c = newRangeCache(t.TempDir(),
		func() (context.Context, func(), error) {
			ctx, cancel := context.WithCancel(context.Background())
			return ctx, func() {
				cancel()
				// The fetcher is on its way out: a player asks for another
				// part of the file right now, and is given the time to ask.
				once.Do(func() {
					go func() { late <- c.WriteRange(context.Background(), &got, from, to) }()
					time.Sleep(500 * time.Millisecond)
				})
			}, nil
		},
		func(ctx context.Context, from int64) (io.ReadCloser, int64, error) {
			return io.NopCloser(bytes.NewReader(file[from:])), int64(len(file)), nil
		})
	defer c.Close()

	var first bytes.Buffer
	if err := c.WriteRange(context.Background(), &first, 0, 3); err != nil || !bytes.Equal(first.Bytes(), file[:4]) {
		t.Fatalf("first range: %v %v", first.Bytes(), err)
	}
	select {
	case err := <-late:
		if err != nil || !bytes.Equal(got.Bytes(), file[from:to+1]) {
			t.Fatalf("late range: %v %v", got.Bytes(), err)
		}
	case <-time.After(75 * time.Second):
		t.Fatal("a range asked while the connection was being released was never served")
	}
}
