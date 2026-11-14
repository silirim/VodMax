package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// rangeCache serves a provider file to a browser through a single
// provider connection. Browsers read a video with several overlapping
// range requests (audio and video far apart in the file, the index at the
// end, seeking); with one connection allowed, each new request cannot
// simply replace the previous one. One fetcher downloads the file in
// chunks into files on disk; requests are served from those chunks and the
// fetcher follows them, jumping only when a request needs a part far from
// where it is reading.

const (
	cacheChunk       = 1 << 20   // bytes per chunk file
	cacheAhead       = 16        // chunks read ahead of each reader
	cacheBatch       = 8         // chunks read before jumping to another region
	cacheBudget      = 512 << 20 // disk per session
	cacheIdleRelease = 20 * time.Second
	cacheStall       = 30 * time.Second // no byte from the provider: reopen
	reopenPause      = 150 * time.Millisecond
)

type rangeCache struct {
	dir     string
	chunk   int64
	ahead   int64
	batch   int
	budget  int64
	stall   time.Duration
	acquire func() (context.Context, func(), error) // the provider connection slot
	open    func(ctx context.Context, from int64) (io.ReadCloser, int64, error)

	mu       sync.Mutex
	size     int64 // total bytes, -1 until known
	have     map[int64]time.Time
	readers  map[int]int64 // reader → chunk it is reading
	waiting  map[int64]int // chunk → readers blocked on it
	nextID   int
	changed  chan struct{} // closed (and replaced) when something changes
	err      error
	closed   bool
	running  bool
	stopSlot func() // ends the fetcher's connection slot, so Close frees it at once
}

func newRangeCache(dir string, acquire func() (context.Context, func(), error),
	open func(ctx context.Context, from int64) (io.ReadCloser, int64, error)) *rangeCache {
	return &rangeCache{dir: dir, chunk: cacheChunk, ahead: cacheAhead, batch: cacheBatch, budget: cacheBudget, stall: cacheStall,
		acquire: acquire, open: open, size: -1, have: map[int64]time.Time{}, readers: map[int]int64{},
		waiting: map[int64]int{}, changed: make(chan struct{})}
}

// broadcast must be called with mu held.
func (c *rangeCache) broadcast() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *rangeCache) chunkPath(k int64) string {
	return filepath.Join(c.dir, fmt.Sprintf("%d.chunk", k))
}

func (c *rangeCache) lastChunk() int64 {
	if c.size < 0 {
		return 1<<62 - 1
	}
	return (c.size - 1) / c.chunk
}

// Close stops the fetcher, even one stuck reading, and removes the chunks.
func (c *rangeCache) Close() {
	c.mu.Lock()
	c.closed = true
	stop := c.stopSlot
	c.broadcast()
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
	os.RemoveAll(c.dir)
}

// Size waits until the file size is known (the first answer of the
// provider) and returns it.
func (c *rangeCache) Size(ctx context.Context, from int64) (int64, error) {
	id := c.register(from / c.chunk)
	defer c.unregister(id)
	for {
		c.mu.Lock()
		size, err, ch := c.size, c.err, c.changed
		c.mu.Unlock()
		if size >= 0 {
			return size, nil
		}
		if err != nil {
			return 0, err
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func (c *rangeCache) register(chunk int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	c.readers[c.nextID] = chunk
	c.startFetcher()
	c.broadcast()
	return c.nextID
}

func (c *rangeCache) unregister(id int) {
	c.mu.Lock()
	delete(c.readers, id)
	c.broadcast()
	c.mu.Unlock()
}

// startFetcher must be called with mu held.
func (c *rangeCache) startFetcher() {
	if !c.running && !c.closed {
		c.running = true
		go c.fetch()
	}
}

// WriteRange copies bytes [from, to] to w.
func (c *rangeCache) WriteRange(ctx context.Context, w io.Writer, from, to int64) error {
	id := c.register(from / c.chunk)
	defer c.unregister(id)
	for off := from; off <= to; {
		k := off / c.chunk
		if err := c.wait(ctx, id, k); err != nil {
			return err
		}
		f, err := os.Open(c.chunkPath(k))
		if err != nil {
			continue // evicted meanwhile: fetched again
		}
		start := off - k*c.chunk
		n := min(c.chunk-start, to-off+1)
		_, err = io.Copy(w, io.NewSectionReader(f, start, n))
		f.Close()
		if err != nil {
			return err
		}
		off += n
	}
	return nil
}

// wait blocks until chunk k is on disk.
func (c *rangeCache) wait(ctx context.Context, id int, k int64) error {
	c.mu.Lock()
	c.readers[id] = k
	if _, ok := c.have[k]; ok {
		c.have[k] = time.Now()
		c.mu.Unlock()
		return nil
	}
	c.waiting[k]++
	c.startFetcher()
	c.broadcast()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.waiting[k]--; c.waiting[k] <= 0 {
			delete(c.waiting, k)
		}
		c.mu.Unlock()
	}()
	for {
		c.mu.Lock()
		_, ok := c.have[k]
		err, closed, ch := c.err, c.closed, c.changed
		if ok {
			c.have[k] = time.Now()
		}
		c.mu.Unlock()
		switch {
		case ok:
			return nil
		case closed:
			return ErrSession
		case err != nil:
			return err
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// target chooses the next chunk to download: keep reading sequentially
// while that serves someone (a blocked reader, or read-ahead), jump to a
// blocked reader after a batch, otherwise fill read-ahead; -1: nothing.
func (c *rangeCache) target(pos int64, sinceJump int) int64 {
	last := c.lastChunk()
	needed := func(k int64) bool {
		if k < 0 || k > last {
			return false
		}
		if _, ok := c.have[k]; ok {
			return false
		}
		for _, r := range c.readers {
			if k >= r && k < r+c.ahead {
				return true
			}
		}
		return false
	}
	if pos >= 0 && needed(pos) && (len(c.waiting) == 0 || c.waiting[pos] > 0 || sinceJump < c.batch) {
		return pos
	}
	if len(c.waiting) > 0 {
		var ks []int64
		for k := range c.waiting {
			if _, ok := c.have[k]; !ok && k <= last {
				ks = append(ks, k)
			}
		}
		if len(ks) > 0 {
			sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
			return ks[0]
		}
	}
	best := int64(-1)
	for _, r := range c.readers {
		for k := r; k < r+c.ahead; k++ {
			if needed(k) && (best < 0 || abs64(k-pos) < abs64(best-pos)) {
				best = k
				break
			}
		}
	}
	return best
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func (c *rangeCache) fetch() {
	var (
		slotCtx   context.Context
		release   func()
		up        io.ReadCloser
		pos       int64 = -1
		sinceJump int
		idleSince = time.Now()
		failures  int
	)
	closeUp := func() {
		if up != nil {
			up.Close()
			up = nil
		}
		pos = -1
	}
	defer func() {
		closeUp()
		if release != nil {
			release()
		}
		c.mu.Lock()
		c.running = false
		// A reader that came while this fetcher was leaving found it still
		// running and started none: start one for it now.
		if c.err == nil && len(c.readers) > 0 {
			c.startFetcher()
		}
		c.mu.Unlock()
	}()
	buf := make([]byte, c.chunk)
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		k := c.target(pos, sinceJump)
		ch := c.changed
		noReaders := len(c.readers) == 0
		c.mu.Unlock()
		if k < 0 {
			if noReaders && time.Since(idleSince) > cacheIdleRelease {
				return // the connection goes back to the account
			}
			closeTimer := time.NewTimer(5 * time.Second)
			select {
			case <-ch:
			case <-closeTimer.C:
			}
			closeTimer.Stop()
			continue
		}
		idleSince = time.Now()
		if slotCtx == nil || slotCtx.Err() != nil {
			if release != nil {
				release()
				release = nil
			}
			c.mu.Lock()
			closed := c.closed
			c.mu.Unlock()
			if closed { // a replaced session must not take the connection back
				return
			}
			acquired, rel, err := c.acquire()
			if err != nil {
				c.fail(err)
				return
			}
			var stop context.CancelFunc
			slotCtx, stop = context.WithCancel(acquired)
			release = func() { stop(); rel() }
			c.mu.Lock()
			c.stopSlot = stop
			if c.closed {
				stop()
			}
			c.mu.Unlock()
		}
		if up == nil || pos != k {
			if up != nil {
				// Jumping: let the provider see the old connection closed
				// before opening the new one (one connection allowed).
				closeUp()
				time.Sleep(reopenPause)
			}
			r, size, err := c.open(slotCtx, k*c.chunk)
			if err != nil {
				if slotCtx.Err() != nil {
					c.fail(errors.New("la connessione all'abbonamento è passata a un altro dispositivo"))
					return
				}
				if failures++; failures > 3 {
					c.fail(err)
					return
				}
				time.Sleep(time.Duration(failures) * 500 * time.Millisecond)
				continue
			}
			up, pos, sinceJump = guardStall(r, c.stall), k, 0
			c.mu.Lock()
			if c.size < 0 && size > 0 {
				c.size = size
				c.broadcast()
			}
			c.mu.Unlock()
		}
		n := c.chunk
		c.mu.Lock()
		if c.size >= 0 {
			n = min(n, c.size-k*c.chunk)
		}
		c.mu.Unlock()
		if _, err := io.ReadFull(up, buf[:n]); err != nil {
			closeUp() // reopened at the same chunk next round
			if failures++; failures > 3 {
				c.fail(err)
				return
			}
			continue
		}
		failures = 0
		tmp := c.chunkPath(k) + ".part"
		if err := os.WriteFile(tmp, buf[:n], 0o600); err != nil {
			c.fail(err)
			return
		}
		os.Rename(tmp, c.chunkPath(k))
		c.mu.Lock()
		c.have[k] = time.Now()
		c.evict()
		c.broadcast()
		c.mu.Unlock()
		pos++
		sinceJump++
	}
}

func (c *rangeCache) fail(err error) {
	c.mu.Lock()
	c.err = err
	c.broadcast()
	c.mu.Unlock()
}

// evict keeps the chunks on disk within budget, dropping the least
// recently used that no reader is about to need. Called with mu held.
func (c *rangeCache) evict() {
	limit := int(c.budget / c.chunk)
	if len(c.have) <= limit {
		return
	}
	type entry struct {
		k  int64
		at time.Time
	}
	var list []entry
	for k, at := range c.have {
		near := false
		for _, r := range c.readers {
			if k >= r-1 && k < r+c.ahead {
				near = true
				break
			}
		}
		if !near {
			list = append(list, entry{k, at})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
	for _, e := range list {
		if len(c.have) <= limit {
			break
		}
		delete(c.have, e.k)
		os.Remove(c.chunkPath(e.k))
	}
}
