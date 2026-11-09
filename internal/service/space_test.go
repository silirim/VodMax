package service

import (
	"context"
	"os"
	"testing"
)

// The Android app's own server has no recorder and runs in Android's
// read-only root, which has 0 bytes free: no "recordings disk full".
func TestNoRecorderNoDiskProblem(t *testing.T) {
	if _, err := os.Stat("/proc/self"); err != nil {
		t.Skip("no /proc")
	}
	t.Chdir("/proc") // like Android's root: a file system with 0 bytes free
	if p := (&Service{}).diskProblems(context.Background()); p != nil {
		t.Fatalf("problems without a recorder: %+v", p)
	}
}
