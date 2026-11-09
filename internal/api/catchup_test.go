package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCatchupSessions(t *testing.T) {
	e, _ := tvSetup(t)
	tv := channels(t, e, "?section=tv")
	var rai, other int64
	for _, c := range tv {
		switch c.Name {
		case "Rai 1":
			rai = c.ID
		case "Canale 5":
			other = c.ID
		}
	}
	now := time.Now()
	ask := func(ch int64, start, stop time.Time) (int, string) {
		code, body := e.do(t, "POST", "/api/v1/catchup/"+strconv.FormatInt(ch, 10)+"/session",
			map[string]any{"viewer": "salotto-tv-01", "start": start.Unix(), "stop": stop.Unix(), "title": "TG1"}, true)
		return code, string(body)
	}
	if code, body := ask(rai, now.Add(-2*time.Hour), now.Add(-time.Hour)); code != 200 ||
		!strings.Contains(body, `"mode":"remux"`) || !strings.Contains(body, `"duration":3600`) || !strings.Contains(body, "TG1 · Rai 1") {
		t.Fatalf("archive: %d %s", code, body)
	}
	// On air: up to now.
	if code, body := ask(rai, now.Add(-30*time.Minute), now.Add(30*time.Minute)); code != 200 || !strings.Contains(body, `"duration":1800`) {
		t.Fatalf("on air: %d %s", code, body)
	}
	for _, c := range []struct {
		name        string
		ch          int64
		start, stop time.Time
		want        string
	}{
		{"no archive", other, now.Add(-2 * time.Hour), now.Add(-time.Hour), "non ha l'archivio"},
		{"too old", rai, now.AddDate(0, 0, -4), now.AddDate(0, 0, -4).Add(time.Hour), "3 giorni"},
		{"future", rai, now.Add(time.Hour), now.Add(2 * time.Hour), "non è ancora iniziato"},
		{"too long", rai, now.Add(-10 * time.Hour), now.Add(-time.Hour), "massimo 6 ore"},
	} {
		if code, body := ask(c.ch, c.start, c.stop); code != 400 || !strings.Contains(body, c.want) {
			t.Errorf("%s: %d %s", c.name, code, body)
		}
	}
}

// A replay or a channel still starting when another device takes the
// connection answers 409 ("take control?") like a busy connection: not a
// failure in the log, and not the provider's address in the answer.
func TestTakenOverWhileStarting(t *testing.T) {
	e, p := tvSetup(t)
	var rai, cielo int64
	for _, c := range channels(t, e, "?section=tv") {
		switch c.Name {
		case "Rai 1":
			rai = c.ID
		case "Cielo":
			cielo = c.ID
		}
	}
	now := time.Now()
	code, body := e.do(t, "POST", "/api/v1/catchup/"+strconv.FormatInt(rai, 10)+"/session",
		map[string]any{"viewer": "salotto-tv-01", "start": now.Add(-2 * time.Hour).Unix(), "stop": now.Add(-time.Hour).Unix()}, true)
	var sess struct{ ID, URL string }
	if code != 200 || json.Unmarshal(body, &sess) != nil {
		t.Fatalf("catch-up: %d %s", code, body)
	}
	// An ffmpeg still reading the archive: nothing written yet.
	dir := t.TempDir()
	ff, started := filepath.Join(dir, "ffmpeg"), filepath.Join(dir, "started")
	if err := os.WriteFile(ff, []byte("#!/bin/sh\ntouch "+started+"\nexec sleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.svc.SetFFmpeg(ff, "")
	p.mu.Lock()
	p.holds = map[string]bool{"101.ts": true}
	p.mu.Unlock()

	var logged bytes.Buffer
	a := &api{svc: e.svc, log: slog.New(slog.NewTextHandler(&logged, nil))}
	open := func(h http.HandlerFunc, path, name, value string) <-chan *httptest.ResponseRecorder {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r := httptest.NewRequestWithContext(ctx, "GET", path, nil)
		r.SetPathValue(name, value)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			defer cancel()
			w := httptest.NewRecorder()
			h(w, r)
			done <- w
		}()
		return done
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("waiting for", what)
			}
		}
	}
	takeOver := func(what string, done <-chan *httptest.ResponseRecorder) {
		t.Helper()
		resp, err := openStream(t, e, ticket(t, e, cielo, "camera-tablet-02")+"?force=1")
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("take over: %v %v", err, resp)
		}
		readSome(t, resp)
		resp.Body.Close()
		w := <-done
		if w.Code != 409 || strings.Contains(w.Body.String(), p.Listener.Addr().String()) || strings.Contains(logged.String(), "level=ERROR") {
			t.Fatalf("%s: %d %s, log %q", what, w.Code, w.Body.String(), logged.String())
		}
	}

	done := open(a.vodRemux, sess.URL+"?ts=1&t=0", "session", sess.ID)
	waitFor("ffmpeg", func() bool { _, err := os.Stat(started); return err == nil })
	takeOver("catch-up", done)

	// The channel: the provider has not answered yet.
	link := ticket(t, e, rai, "salotto-tv-01")
	done = open(a.liveStream, link+"?force=1", "ticket", strings.TrimPrefix(link, "/stream/live/"))
	waitFor("the provider", func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.hits["101.ts"] > 0 })
	takeOver("channel", done)
}
