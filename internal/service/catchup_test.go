package service

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/playlist/xtream"
)

// "Start over" of a programme on air: each opening asks the archive up to
// the moment it is made, so going on from where the first one ended
// gets what aired meanwhile.
func TestCatchupOnAirAsksUpToNow(t *testing.T) {
	s, sub := oneConnectionService(t)
	ff := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ff, []byte("#!/bin/sh\nprintf 'ftypisom'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.SetFFmpeg(ff, "")
	client, err := xtream.New("http://panel.invalid", "u", "p", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-30 * time.Minute)
	sess := &vodSession{id: "sessdddd", viewer: "salotto-tv-01", media: "catchup", subID: sub, mode: "remux",
		internal: "tokendddd", expires: time.Now().Add(time.Hour),
		catchup: &catchupInfo{client: client, streamID: "101", start: start, stop: start.Add(time.Hour), loc: time.UTC},
		probe:   &probeResult{VideoCodec: "h264", Audio: []Track{}, Subtitles: []Track{}}}
	s.addVODSession(sess)
	for _, c := range []struct {
		at   float64
		want string // minutes asked
	}{{0, "/31/"}, {20 * 60, "/11/"}} {
		st, err := s.OpenRemux(context.Background(), sess.id, c.at, false, -1, -1)
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
		s.vod.mu.Lock()
		url := sess.url
		s.vod.mu.Unlock()
		if !strings.Contains(url, "/timeshift/u/p"+c.want) {
			t.Errorf("at %v s: archive asked %s, want minutes %s", c.at, url, c.want)
		}
	}
}
