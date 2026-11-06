package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/service"
)

type recRow struct {
	ID     int64
	Title  string
	Status string
	Start  int64
	Stop   int64
	Bytes  int64
	Error  string
	RuleID int64 `json:"rule_id"`
}

func recRows(t *testing.T, e *env) []recRow {
	t.Helper()
	_, body := e.do(t, "GET", "/api/v1/recordings", nil, true)
	var l []recRow
	if err := json.Unmarshal(body, &l); err != nil {
		t.Fatalf("recordings: %s", body)
	}
	return l
}

// A channel the provider no longer serves: the recording does not cut the
// TV of whoever is watching every few seconds for the whole programme,
// and asks the provider less and less often.
func TestBrokenRecordingLeavesTheViewerAlone(t *testing.T) {
	e, p := tvSetup(t)
	p.mu.Lock()
	p.broken = map[string]bool{"101.ts": true}
	p.mu.Unlock()
	tv := channels(t, e, "?section=tv")
	rai, cielo := tv[0], tv[2]
	open := func() io.ReadCloser {
		for range 10 {
			v, err := openStream(t, e, ticket(t, e, cielo.ID, "salotto-tv-01"))
			if err == nil && v.StatusCode == 200 {
				return v.Body
			}
			if v != nil {
				v.Body.Close()
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatal("the viewer cannot watch")
		return nil
	}
	viewer := open()
	now := time.Now().Unix()
	e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 3600, "title": "Rotto"}, true)
	cut := 0
	deadline := time.Now().Add(14 * time.Second)
	for time.Now().Before(deadline) {
		done := make(chan error, 1)
		go func(v io.Reader) { _, err := io.Copy(io.Discard, v); done <- err }(viewer)
		select {
		case <-done:
			cut++
			viewer.Close()
			viewer = open() // the family presses OK again
		case <-time.After(time.Until(deadline)):
		}
	}
	viewer.Close()
	p.mu.Lock()
	hits := p.hits["101.ts"]
	p.mu.Unlock()
	if cut > 1 || hits > 3 {
		t.Fatalf("viewer cut %d times, broken channel asked %d times in 14 s", cut, hits)
	}
}

// A full disk ends the recording at once, keeping what it has: no
// reconnections to the provider every few seconds.
func TestRecordingStopsOnAFullDisk(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	e, p := tvSetup(t)
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now()
	os.MkdirAll(e.recordings, 0o700)
	name := fmt.Sprintf("%d-%s.ts", 1, now.Format("20060102-1504"))
	if err := os.Symlink("/dev/full", filepath.Join(e.recordings, name)); err != nil {
		t.Fatal(err)
	}
	e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now.Unix(), "stop": now.Unix() + 3600, "title": "Pieno"}, true)
	time.Sleep(5 * time.Second)
	p.mu.Lock()
	hits := p.hits["101.ts"]
	p.mu.Unlock()
	l := recRows(t, e)
	if hits != 1 || len(l) != 1 || l[0].Status != "failed" || !strings.Contains(l[0].Error, "disco") {
		t.Fatalf("full disk: provider asked %d times, %+v", hits, l)
	}
}

// The provider closes the recording a moment after every reconnection, as
// when another device uses the account: the recording keeps reconnecting
// at once, and the status and the recording say what is probably going
// on.
func TestContendedRecordingSaysSo(t *testing.T) {
	e, p := tvSetup(t)
	p.mu.Lock()
	p.kicks = map[string]bool{"101.ts": true}
	p.mu.Unlock()
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now().Unix()
	e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 9, "title": "Contesa"}, true)
	time.Sleep(7 * time.Second)
	_, body := e.do(t, "GET", "/api/v1/status", nil, true)
	if !strings.Contains(string(body), "forse un altro dispositivo usa l'abbonamento") || !strings.Contains(string(body), "Contesa") {
		t.Fatalf("status: %s", body)
	}
	time.Sleep(4 * time.Second)
	p.mu.Lock()
	hits := p.hits["101.ts"]
	p.mu.Unlock()
	l := recRows(t, e)
	if hits < 3 || len(l) != 1 || l[0].Status != "completed" || !strings.Contains(l[0].Error, "(forse un altro dispositivo usa l'abbonamento)") {
		t.Fatalf("contended: %d connections, %+v", hits, l)
	}
}

// A recording in progress counts only the time with data: after a
// reconnection its length (and so where a seek lands) leaves out the
// seconds without a stream.
func TestRecordingLengthLeavesOutGaps(t *testing.T) {
	e, p := tvSetup(t)
	e.svc.SetRecordStall(300 * time.Millisecond)
	p.mu.Lock()
	p.stalls = map[string]bool{"101.ts": true}
	p.mu.Unlock()
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now().Unix()
	_, body := e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 30, "title": "Buchi"}, true)
	var rec struct{ ID int64 }
	json.Unmarshal(body, &rec)
	time.Sleep(4 * time.Second) // ~0.1 s of data, a stall, 3 s of pause, a second connection
	_, body = e.do(t, "POST", fmt.Sprintf("/api/v1/recordings/%d/ticket", rec.ID), nil, true)
	var tk struct{ Recorded *float64 }
	if json.Unmarshal(body, &tk) != nil || tk.Recorded == nil || *tk.Recorded > 1 {
		t.Fatalf("ticket after 4 s with 2 short connections: %s", body)
	}
}

// slowFFmpeg converts a recording in about 3 s (it copies the file).
func slowFFmpeg(t *testing.T) string {
	t.Helper()
	ff := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nprev=; for a; do [ \"$prev\" = -i ] && in=\"$a\"; prev=\"$a\"; last=\"$a\"; done\nsleep 3\ncp \"$in\" \"$last\"\n"
	if err := os.WriteFile(ff, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return ff
}

func recFiles(t *testing.T, e *env) []string {
	t.Helper()
	entries, _ := os.ReadDir(e.recordings)
	var names []string
	for _, x := range entries {
		names = append(names, x.Name())
	}
	return names
}

// Stopping a recording answers once it is saved, without waiting for the
// conversion to MP4; deleting it while it converts leaves no file behind.
func TestRecordingConversionAfterTheSave(t *testing.T) {
	e, _ := tvSetup(t)
	e.svc.SetFFmpeg(slowFFmpeg(t), "")
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now().Unix()
	_, body := e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 600, "title": "Ferma"}, true)
	var rec struct{ ID int64 }
	json.Unmarshal(body, &rec)
	time.Sleep(time.Second)
	began := time.Now()
	if code, body := e.do(t, "POST", fmt.Sprintf("/api/v1/recordings/%d/stop", rec.ID), nil, true); code != 204 || time.Since(began) > 2*time.Second {
		t.Fatalf("stop: %d %s after %v", code, body, time.Since(began))
	}
	if l := recRows(t, e); len(l) != 1 || l[0].Status != "completed" {
		t.Fatalf("while converting: %+v", l)
	}
	time.Sleep(4 * time.Second)
	if names := recFiles(t, e); len(names) != 1 || !strings.HasSuffix(names[0], ".mp4") {
		t.Fatalf("after the conversion: %v", names)
	}

	// Deleted in the middle of its conversion.
	now = time.Now().Unix()
	_, body = e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 2, "title": "Breve"}, true)
	json.Unmarshal(body, &rec)
	time.Sleep(3500 * time.Millisecond) // ended, converting
	if code, body := e.do(t, "DELETE", fmt.Sprintf("/api/v1/recordings/%d", rec.ID), nil, true); code != 204 {
		t.Fatalf("delete while converting: %d %s", code, body)
	}
	time.Sleep(4 * time.Second)
	if l, names := recRows(t, e), recFiles(t, e); len(l) != 1 || len(names) != 1 {
		t.Fatalf("after deleting during the conversion: %+v, files %v", l, names)
	}
}

// A restart while a stopped recording converts: it stays stopped and
// finished, it does not start again.
func TestRestartDuringConversionKeepsTheRecordingStopped(t *testing.T) {
	e, _ := tvSetup(t)
	e.svc.SetFFmpeg(slowFFmpeg(t), "")
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now().Unix()
	_, body := e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now, "stop": now + 600, "title": "Ferma"}, true)
	var rec struct{ ID int64 }
	json.Unmarshal(body, &rec)
	time.Sleep(time.Second)
	e.do(t, "POST", fmt.Sprintf("/api/v1/recordings/%d/stop", rec.ID), nil, true)
	e.stop()
	e.start()
	time.Sleep(6 * time.Second)
	if l := recRows(t, e); len(l) != 1 || l[0].Status != "completed" || !strings.Contains(l[0].Error, "prima della fine") || l[0].Stop > time.Now().Unix() {
		t.Fatalf("after the restart: %+v", l)
	}
}

// fakeConverter stands in for ffmpeg: the copy of everything fails as with
// a broken audio frame when copyFails, and the MP4 it writes is the TS
// (or 10 bytes, when truncates).
func fakeConverter(t *testing.T, copyFails, truncates bool) string {
	t.Helper()
	script := "#!/bin/sh\nprev=; for a; do [ \"$prev\" = -i ] && in=\"$a\"; [ \"$prev\" = -c ] && all=\"$a\"; prev=\"$a\"; last=\"$a\"; done\n"
	if copyFails {
		script += "if [ \"$all\" = copy ]; then\n echo '[h264 @ 0x1] non-existing PPS 0 referenced' >&2\n" +
			" echo '[aac_adtstoasc @ 0x2] Error parsing ADTS frame header!' >&2\n echo 'partial' > \"$last\"\n exit 1\nfi\n"
	}
	if truncates {
		script += "printf 0123456789 > \"$last\"\n"
	} else {
		script += "cp \"$in\" \"$last\"\n"
	}
	ff := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ff, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return ff
}

func recordFor(t *testing.T, e *env, secs int64, title string) {
	t.Helper()
	rai := channels(t, e, "?section=tv")[0]
	now := time.Now().Unix()
	if code, body := e.do(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": rai.ID, "start": now, "stop": now + secs, "title": title}, true); code != 201 {
		t.Fatalf("schedule: %d %s", code, body)
	}
}

// The copy into MP4 fails on a broken audio frame: the conversion is
// done again converting the audio, and the TS goes only then.
func TestConversionConvertsTheAudioWhenTheCopyFails(t *testing.T) {
	e, _ := tvSetup(t)
	e.svc.SetFFmpeg(fakeConverter(t, true, false), "")
	recordFor(t, e, 2, "Partita")
	time.Sleep(4 * time.Second)
	if names := recFiles(t, e); len(names) != 1 || !strings.HasSuffix(names[0], ".mp4") {
		t.Fatalf("files: %v", names)
	}
}

// ffmpeg ends without an error but writes a fraction of the recording:
// the TS stays, the MP4 goes.
func TestTruncatedMP4KeepsTheTS(t *testing.T) {
	e, _ := tvSetup(t)
	e.svc.SetFFmpeg(fakeConverter(t, false, true), "")
	recordFor(t, e, 2, "Partita")
	time.Sleep(4 * time.Second)
	if names := recFiles(t, e); len(names) != 1 || !strings.HasSuffix(names[0], ".ts") {
		t.Fatalf("files: %v", names)
	}
	if l := recRows(t, e); len(l) != 1 || l[0].Status != "completed" || l[0].Bytes < 188*10 {
		t.Fatalf("recording: %+v", l)
	}
}

// A recording left in MPEG-TS (an older version, a failed conversion)
// becomes an MP4 at the next start.
func TestLeftoverTSConvertedAtStart(t *testing.T) {
	e, _ := tvSetup(t)
	e.svc.SetFFmpeg(fakeConverter(t, false, true), "") // fails the check: stays TS
	recordFor(t, e, 2, "Vecchia")
	time.Sleep(4 * time.Second)
	if names := recFiles(t, e); len(names) != 1 || !strings.HasSuffix(names[0], ".ts") {
		t.Fatalf("before the restart: %v", names)
	}
	e.stop()
	good := fakeConverter(t, false, false)
	e.prepare = func(s *service.Service) { s.SetFFmpeg(good, "") }
	e.start()
	time.Sleep(time.Second)
	names := recFiles(t, e)
	if len(names) != 1 || !strings.HasSuffix(names[0], ".mp4") {
		t.Fatalf("after the restart: %v", names)
	}
	_, body := e.do(t, "GET", "/api/v1/recordings", nil, true)
	var l []struct{ ID int64 }
	json.Unmarshal(body, &l)
	_, body = e.do(t, "POST", fmt.Sprintf("/api/v1/recordings/%d/ticket", l[0].ID), nil, true)
	var tk struct{ URL string }
	json.Unmarshal(body, &tk)
	resp, err := http.Get(e.srv.URL + tk.URL)
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("playing the converted recording: %v %v", err, resp)
	}
	resp.Body.Close()
}
