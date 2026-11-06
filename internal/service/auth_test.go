package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/store"
)

// Parallel PIN attempts from one device are counted before the slow check:
// a burst tests at most pinMaxFails PINs, then the device waits.
func TestPINLimitHoldsUnderParallelAttempts(t *testing.T) {
	s, _ := localTestService(t)
	hash, err := hashPIN("2468")
	if err != nil {
		t.Fatal(err)
	}
	p := store.Profile{ID: 1, HasPIN: true, PINHash: hash}
	var mu sync.Mutex
	got := map[error]int{}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.tryPIN(7, p, fmt.Sprintf("%04d", i))
			mu.Lock()
			got[err]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if got[ErrPIN] != pinMaxFails || got[ErrPINLocked] != 100-pinMaxFails {
		t.Fatalf("100 parallel wrong PINs: %v (want %d checked, the rest locked)", got, pinMaxFails)
	}
	if err := s.tryPIN(7, p, "2468"); !errors.Is(err, ErrPINLocked) {
		t.Fatalf("right PIN while locked: %v", err)
	}
	if err := s.tryPIN(8, p, "2468"); err != nil {
		t.Fatalf("another device: %v", err)
	}
}

// A paired device's request does not wait for the database writer (held
// for seconds by a catalog or guide import) to record its activity.
func TestAuthenticateDoesNotWaitForWriter(t *testing.T) {
	s, dir := localTestService(t)
	ctx := context.Background()
	id, err := s.st.AddDevice(ctx, "TV", hashToken("device-token"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", "file:"+filepath.Join(filepath.Dir(dir), "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.BeginTx(ctx, nil)
	if err == nil {
		_, err = tx.Exec(`UPDATE device SET name = name`) // takes the write lock
	}
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := s.Authenticate(ctx, "device-token", "", "master")
	if err != nil || got.DeviceID != id {
		t.Fatal(got, err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("request waited %v for the writer", d)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if d, _ := s.st.DeviceByToken(ctx, hashToken("device-token")); d.LastSeen > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("activity never recorded")
}
