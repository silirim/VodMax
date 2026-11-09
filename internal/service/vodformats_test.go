package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// A provider that answers a film with an HLS playlist whose segments are
// elsewhere: ffmpeg and ffprobe must refuse it rather than fetch the
// segments themselves, outside the subscription's connection.
func TestVODRefusesPlaylists(t *testing.T) {
	s, sub := oneConnectionService(t)
	var outside atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outside.Add(1) }))
	defer elsewhere.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2.0,\n%s/seg0.ts\n#EXT-X-ENDLIST\n", elsewhere.URL)
	}))
	defer provider.Close()
	sess := &vodSession{id: "sesseeee", viewer: "salotto-tv-01", media: "movie", subID: sub, url: provider.URL + "/movie/u/p/1.mkv",
		mode: "remux", internal: "tokeneeee", expires: time.Now().Add(time.Hour),
		probe: &probeResult{VideoCodec: "h264", Audio: []Track{}, Subtitles: []Track{}}}
	s.addVODSession(sess)
	ran := false
	if _, err := exec.LookPath(s.ffmpegBin()); err == nil {
		ran = true
		if st, err := s.OpenRemux(context.Background(), sess.id, 0, false, -1, -1, false); err == nil {
			st.Close()
			t.Error("ffmpeg converted a playlist")
		}
	}
	if _, err := exec.LookPath(s.ffprobeBin()); err == nil {
		ran = true
		if _, err := s.probe(context.Background(), sess, false); err == nil {
			t.Error("ffprobe analysed a playlist")
		}
	}
	if !ran {
		t.Skip("neither ffmpeg nor ffprobe installed")
	}
	if n := outside.Load(); n > 0 {
		t.Fatalf("segments fetched outside the subscription's connection: %d", n)
	}
}
