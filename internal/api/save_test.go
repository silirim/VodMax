package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/store"
)

type saveLinkReply struct {
	URL, Name, MIME, ETag string
	Size                  int64
}

func fetch(t *testing.T, method, url string, header ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// Copies on a device (0056): a film downloaded from the provider, an
// episode of a collection and a recording, with resumable links that a PIN
// change, a new file or removing the device end. The provider is not
// asked anything, and the token never reaches the log.
func TestSaveOnDevice(t *testing.T) {
	e, p := tvSetup(t)
	ctx := context.Background()
	addProfile := func(body map[string]any) int64 {
		code, out := e.do(t, "POST", "/api/v1/profiles", body, true)
		var r struct{ ID int64 }
		if code != 201 || json.Unmarshal(out, &r) != nil {
			t.Fatalf("add profile: %d %s", code, out)
		}
		return r.ID
	}
	e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": "9753"}, true)
	marco := addProfile(map[string]any{"name": "Marco", "kind": "standard"})
	kids := addProfile(map[string]any{"name": "Bimbi", "kind": "kids"})
	phone := pair(t, e, "Telefono", nil)
	_, marcoTok := selectProfile(t, e, phone, marco, "")
	_, kidsTok := selectProfile(t, e, phone, kids, "")
	save := func(device, profile, media string, id int64, want int) saveLinkReply {
		t.Helper()
		code, body := e.call(t, "POST", fmt.Sprintf("/api/v1/save/%s/%d", media, id), nil, device, profile)
		var l saveLinkReply
		json.Unmarshal(body, &l)
		if code != want {
			t.Fatalf("save %s %d: %d %s", media, id, code, body)
		}
		return l
	}
	film := findItem(t, e, "movie", "1408")

	// The device on its own downloads nothing, so it copies nothing.
	save(phone, marcoTok, "movie", film, 400)

	dir := t.TempDir()
	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	if err := e.svc.StartDownloads(runCtx, dir, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	e.do(t, "POST", "/api/v1/downloads", map[string]any{"media": "movie", "id": film}, true)
	waitDownloads(t, e, 1)
	series := t.TempDir()
	put(t, series, "Serie di casa/Stagione 1/S01E02 Fine.mkv", fakeFilm[:5000])
	_, body := e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "Serie di casa", "kind": "local", "path": series, "content": "series"}, true)
	var sub struct{ ID int64 }
	json.Unmarshal(body, &sub)
	if code, body := e.do(t, "POST", fmt.Sprintf("/api/v1/subscriptions/%d/sync?wait=true", sub.ID), nil, true); code != 200 {
		t.Fatalf("collection: %d %s", code, body)
	}
	st, err := store.Open(e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p.mu.Lock()
	before := p.requests
	p.mu.Unlock()

	// Not for the admin token, not for kids; only what is on the disk.
	if code, _ := e.call(t, "POST", fmt.Sprintf("/api/v1/save/movie/%d", film), nil, token, ""); code != 403 {
		t.Fatalf("admin token: %d", code)
	}
	save(phone, kidsTok, "movie", film, 403)
	save(phone, marcoTok, "movie", findItem(t, e, "movie", "psycho"), 409)
	save(phone, marcoTok, "show", film, 400)

	l := save(phone, marcoTok, "movie", film, 200)
	if l.Name != "1408 (2007).mp4" || l.MIME != "video/mp4" || l.Size != int64(len(fakeFilm)) || !strings.HasPrefix(l.URL, "/stream/save/") ||
		!strings.HasPrefix(l.ETag, `"`) {
		t.Fatalf("link: %+v", l)
	}
	url := e.srv.URL + l.URL
	resp, body := fetch(t, "GET", url)
	h := resp.Header
	if resp.StatusCode != 200 || !bytes.Equal(body, fakeFilm) || h.Get("ETag") != l.ETag || h.Get("Content-Type") != "video/mp4" ||
		h.Get("Cache-Control") != "private, no-store" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Disposition") != `attachment; filename="1408 (2007).mp4"` {
		t.Fatalf("file: %d %d bytes %v", resp.StatusCode, len(body), h)
	}
	// Resuming: the same file gives the rest, another one all of it.
	if resp, body := fetch(t, "GET", url, "Range", "bytes=1000-", "If-Range", l.ETag); resp.StatusCode != 206 || !bytes.Equal(body, fakeFilm[1000:]) ||
		resp.Header.Get("Content-Range") != fmt.Sprintf("bytes 1000-%d/%d", len(fakeFilm)-1, len(fakeFilm)) {
		t.Fatalf("resume: %d %v", resp.StatusCode, resp.Header)
	}
	if resp, body := fetch(t, "GET", url, "Range", "bytes=1000-", "If-Range", `"altro"`); resp.StatusCode != 200 || len(body) != len(fakeFilm) {
		t.Fatalf("If-Range of another file: %d", resp.StatusCode)
	}
	if resp, _ := fetch(t, "GET", url, "If-Match", `"altro"`); resp.StatusCode != 412 {
		t.Fatalf("If-Match of another file: %d", resp.StatusCode)
	}
	if resp, _ := fetch(t, "GET", url, "Range", fmt.Sprintf("bytes=%d-", len(fakeFilm))); resp.StatusCode != 416 {
		t.Fatalf("range past the end: %d", resp.StatusCode)
	}
	if resp, body := fetch(t, "HEAD", url); resp.StatusCode != 200 || len(body) != 0 || resp.ContentLength != int64(len(fakeFilm)) {
		t.Fatalf("HEAD: %d %d", resp.StatusCode, resp.ContentLength)
	}
	forged := strings.TrimSuffix(l.URL, "A") + "B"
	if strings.HasSuffix(l.URL, "B") {
		forged = strings.TrimSuffix(l.URL, "B") + "A"
	}
	for _, bad := range []string{forged, "/stream/save/x", strings.Replace(l.URL, "/movie.", "/episode.", 1)} {
		if resp, _ := fetch(t, "GET", e.srv.URL+bad); resp.StatusCode != 404 {
			t.Fatalf("forged %s: %d", bad, resp.StatusCode)
		}
	}

	// An episode of a collection, in the series' folder.
	_, body = e.do(t, "GET", fmt.Sprintf("/api/v1/series/%d/episodes", findItem(t, e, "series", "casa")), nil, true)
	var eps []struct{ ID int64 }
	if json.Unmarshal(body, &eps) != nil || len(eps) != 1 {
		t.Fatalf("episodes: %s", body)
	}
	ep := save(phone, marcoTok, "episode", eps[0].ID, 200)
	if !strings.HasPrefix(ep.Name, "Serie di casa/Serie di casa S01E02") || !strings.HasSuffix(ep.Name, ".mkv") || ep.MIME != "video/x-matroska" || ep.Size != 5000 {
		t.Fatalf("episode: %+v", ep)
	}
	if resp, body := fetch(t, "GET", e.srv.URL+ep.URL); resp.StatusCode != 200 || !bytes.Equal(body, fakeFilm[:5000]) {
		t.Fatalf("episode file: %d", resp.StatusCode)
	}

	// A finished recording; once it becomes an MP4 its link is gone (410)
	// and a new one gives the MP4.
	recDir := t.TempDir()
	start := time.Now().Add(time.Hour).Unix() // not due: the recorder leaves it alone
	addRec := func(file string) int64 {
		id, err := st.AddRecording(ctx, store.Recording{ItemID: 1, GroupKey: "rai1", Channel: "Rai 1", Title: "TG1", Start: start, Stop: start + 1800})
		if err == nil {
			err = st.UpdateRecording(ctx, store.Recording{ID: id, Status: "completed", File: file, Stop: start + 1800})
		}
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ts := filepath.Join(recDir, "1-tg1.ts")
	os.WriteFile(ts, bytes.Repeat([]byte{0x47}, 188*10), 0o644)
	rec := addRec(ts)
	rl := save(phone, marcoTok, "recording", rec, 200)
	if rl.Name != "Registrazioni/TG1 - Rai 1 "+time.Unix(start, 0).Format("2006-01-02 1504")+".ts" || rl.MIME != "video/mp2t" {
		t.Fatalf("recording: %+v", rl)
	}
	notes := filepath.Join(recDir, "note.srt")
	os.WriteFile(notes, []byte("1\n"), 0o644)
	save(phone, marcoTok, "recording", addRec(notes), 409)
	mp4 := strings.TrimSuffix(ts, ".ts") + ".mp4"
	os.WriteFile(mp4, fakeFilm[:3000], 0o644)
	if ok, err := st.SetRecordingFile(ctx, rec, ts, mp4, 3000); !ok || err != nil {
		t.Fatal("switch to MP4:", err)
	}
	os.Remove(ts)
	var logged bytes.Buffer
	a := &api{svc: e.svc, log: slog.New(slog.NewTextHandler(&logged, nil))}
	direct := func(link string) int {
		r := httptest.NewRequest("GET", link, nil)
		r.SetPathValue("token", strings.TrimPrefix(link, "/stream/save/"))
		w := httptest.NewRecorder()
		a.saveFile(w, r)
		return w.Code
	}
	if code := direct(rl.URL); code != 410 {
		t.Fatalf("recording turned MP4: %d", code)
	}
	if l := save(phone, marcoTok, "recording", rec, 200); !strings.HasSuffix(l.Name, ".mp4") || l.MIME != "video/mp4" || l.Size != 3000 {
		t.Fatalf("recording as MP4: %+v", l)
	}

	// A PIN on the profile ends its links.
	if code, body := e.do(t, "PATCH", "/api/v1/profiles/"+strconv.FormatInt(marco, 10), map[string]any{"pin": "2468"}, true); code != 204 {
		t.Fatalf("PIN: %d %s", code, body)
	}
	if code := direct(l.URL); code != 404 {
		t.Fatalf("link after a PIN change: %d", code)
	}
	_, marcoTok = selectProfile(t, e, phone, marco, "2468")
	l = save(phone, marcoTok, "movie", film, 200)

	// Another file on the server: 410; its new link works.
	movie := filepath.Join(dir, "Movies", "1408 (2007)", "1408 (2007).mp4")
	os.WriteFile(movie, fakeFilm[:2000], 0o644)
	if code := direct(l.URL); code != 410 {
		t.Fatalf("changed file: %d", code)
	}
	l = save(phone, marcoTok, "movie", film, 200)
	if code := direct(l.URL); code != 200 {
		t.Fatalf("new link: %d", code)
	}
	// A file the server cannot read: 500, and the log names the title, not the token.
	if os.Geteuid() != 0 {
		os.Chmod(movie, 0)
		if code := direct(l.URL); code != 500 || !strings.Contains(logged.String(), "media=movie id="+strconv.FormatInt(film, 10)) {
			t.Fatalf("unreadable file: %d %s", code, logged.String())
		}
		os.Chmod(movie, 0o644)
	}
	if strings.Contains(logged.String(), "save/") || strings.Contains(logged.String(), strings.TrimPrefix(l.URL, "/stream/save/")) {
		t.Fatalf("token in the log: %s", logged.String())
	}

	// Removing the device ends its links, also when a new device gets its id.
	var devs []struct {
		ID   int64
		Name string
	}
	_, body = e.do(t, "GET", "/api/v1/devices", nil, true)
	json.Unmarshal(body, &devs)
	var phoneID int64
	for _, d := range devs {
		if d.Name == "Telefono" {
			phoneID = d.ID
		}
	}
	if code, _ := e.do(t, "DELETE", "/api/v1/devices/"+strconv.FormatInt(phoneID, 10), nil, true); code != 204 {
		t.Fatalf("remove device: %d", code)
	}
	if code := direct(l.URL); code != 404 {
		t.Fatalf("link of a removed device: %d", code)
	}
	for now := time.Now().Unix(); time.Now().Unix() == now; {
		time.Sleep(50 * time.Millisecond) // another pairing date
	}
	pair(t, e, "Tablet", nil)
	if code := direct(l.URL); code != 404 {
		t.Fatalf("link after the device id was reused: %d", code)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.requests != before {
		t.Fatalf("the provider was asked %d times", p.requests-before)
	}
}
