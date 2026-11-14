package xtream

import (
	"encoding/json"
	"testing"
)

// Panels that fill duration_secs with an empty or zero value still give
// the series' episode_run_time in minutes: it must become seconds.
func TestDetailsRunTimeWithEmptyDurationSecs(t *testing.T) {
	for _, raw := range []string{
		`{"episode_run_time":"45","duration_secs":""}`,
		`{"episode_run_time":"45","duration_secs":0}`,
		`{"episode_run_time":45,"duration_secs":"0"}`,
	} {
		if got := ParseDetails(json.RawMessage(raw)).DurationSecs; got != 45*60 {
			t.Errorf("%s: DurationSecs = %d, want %d", raw, got, 45*60)
		}
	}
	// A real duration_secs still wins and is not scaled.
	if got := ParseDetails(json.RawMessage(`{"episode_run_time":"45","duration_secs":2700}`)).DurationSecs; got != 2700 {
		t.Errorf("duration_secs 2700: got %d", got)
	}
}
