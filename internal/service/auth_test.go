package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"

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
