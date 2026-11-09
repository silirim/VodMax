package service

import (
	"context"
	"errors"
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
		st, err := s.OpenRemux(context.Background(), sess.id, c.at, false, -1, -1, false)
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

// In the browser a catch-up is MPEG-TS for mpegts.js, sent at playing
// speed after the skipped seconds; the Android player keeps the MP4.
func TestCatchupRemuxTS(t *testing.T) {
	s, sub := oneConnectionService(t)
	dir := t.TempDir()
	ff, argsFile := filepath.Join(dir, "ffmpeg"), filepath.Join(dir, "args")
	if err := os.WriteFile(ff, []byte("#!/bin/sh\necho \"$*\" > "+argsFile+"\nprintf 'GGGGGGGG'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.SetFFmpeg(ff, "")
	client, err := xtream.New("http://panel.invalid", "u", "p", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-30 * time.Minute)
	sess := &vodSession{id: "sessffff", viewer: "salotto-tv-01", media: "catchup", subID: sub, mode: "remux",
		internal: "tokenffff", expires: time.Now().Add(time.Hour),
		catchup: &catchupInfo{client: client, streamID: "101", start: start, stop: start.Add(time.Hour), loc: time.UTC},
		probe:   &probeResult{VideoCodec: "h264", Audio: []Track{}, Subtitles: []Track{}}}
	s.addVODSession(sess)
	for _, c := range []struct {
		ts          bool
		ctype       string
		want, avoid []string
	}{
		{true, "video/mp2t", []string{"-readrate 1 -readrate_initial_burst 45 -i ", "-ss 15.0", "-c:v copy", "-f mpegts pipe:1"}, []string{"empty_moov"}},
		{false, "video/mp4", []string{"-ss 15.0", "-c:v copy", "-f mp4 -movflags frag_keyframe+empty_moov+default_base_moof pipe:1"}, []string{"-readrate", "mpegts pipe:1"}},
	} {
		st, err := s.OpenRemux(context.Background(), sess.id, 75, false, -1, -1, c.ts)
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
		raw, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		args := string(raw)
		if st.ContentType != c.ctype {
			t.Errorf("ts %v: content type %q", c.ts, st.ContentType)
		}
		for _, w := range c.want {
			if !strings.Contains(args, w) {
				t.Errorf("ts %v: %q missing in %s", c.ts, w, args)
			}
		}
		for _, a := range c.avoid {
			if strings.Contains(args, a) {
				t.Errorf("ts %v: %q in %s", c.ts, a, args)
			}
		}
	}
}

// A conversion replaced while it starts says so (410), not "conversion
// failed" (500).
func TestRemuxReplacedWhileStarting(t *testing.T) {
	s, _ := oneConnectionService(t)
	ff := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ff, []byte("#!/bin/sh\nexec sleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.SetFFmpeg(ff, "")
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(300*time.Millisecond, func() { cancel(ErrReplaced) })
	if _, err := s.startRemux(ctx, nil, nil, TranscodeSettings{}, true); !errors.Is(err, ErrReplaced) {
		t.Fatalf("got %v", err)
	}
}

// The browser's MPEG-TS catch-up is offered only with an ffmpeg that can
// pace it (6.1 and later); 5.1 (Debian 12) refuses the option.
func TestPacedRemux(t *testing.T) {
	s := &Service{}
	for _, c := range []struct {
		help string
		want bool
	}{
		{"-readrate speed  read input at specified rate\n-readrate_initial_burst seconds  The initial amount of input to burst read\n", true},
		{"-readrate speed  read input at specified rate\n", false},
	} {
		ff := filepath.Join(t.TempDir(), "ffmpeg")
		if err := os.WriteFile(ff, []byte("#!/bin/sh\nprintf '%s' '"+c.help+"'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		s.SetFFmpeg(ff, "")
		if got := s.pacedRemux(); got != c.want {
			t.Errorf("%q: got %v", c.help, got)
		}
	}
	s.SetFFmpeg(filepath.Join(t.TempDir(), "none"), "")
	if s.pacedRemux() {
		t.Error("no ffmpeg: paced")
	}
}

// ffmpeg does not change while the server runs: its answer is kept until
// SetFFmpeg, but a probe that failed is asked again.
func TestPacedRemuxKept(t *testing.T) {
	s := &Service{}
	ff := filepath.Join(t.TempDir(), "ffmpeg")
	s.SetFFmpeg(ff, "")
	if s.pacedRemux() {
		t.Fatal("no ffmpeg: paced")
	}
	if err := os.WriteFile(ff, []byte("#!/bin/sh\nprintf '%s\\n' '-readrate_initial_burst seconds'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !s.pacedRemux() {
		t.Fatal("the failed probe was kept")
	}
	os.Remove(ff)
	if !s.pacedRemux() {
		t.Fatal("ffmpeg asked again")
	}
	s.SetFFmpeg(ff, "")
	if s.pacedRemux() {
		t.Fatal("answer kept after SetFFmpeg")
	}
}
