package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func upload(t *testing.T, e *env, method, path, body, lang string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-VodMax-Lang", lang)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// An M3U playlist from a file (0057): uploaded as the request body,
// replaced from Modifica, never given back, kept by the backup.
func TestPlaylistFileUpload(t *testing.T) {
	e := setup(t, true)
	if code, body := e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "Proton IT", "config": wgConf(t)}, true); code != 201 {
		t.Fatalf("vpn: %d %s", code, body)
	}
	list := "#EXTM3U\n#EXTINF:-1 tvg-id=\"a\",Canale A\nhttp://panel.example/u/p/a.ts\n#EXTINF:-1 tvg-id=\"b\",Canale B\nhttp://panel.example/u/p/b.ts\n"
	if code, body := upload(t, e, "POST", "/api/v1/subscriptions/file?name=Da%20file&vpn_id=1&file=lista.m3u", list, "it"); code != 201 {
		t.Fatalf("upload: %d %s", code, body)
	}
	if code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync?wait=true", nil, true); code != 200 || !strings.Contains(string(body), `"live":2`) {
		t.Fatalf("sync: %d %s", code, body)
	}
	_, body := e.do(t, "GET", "/api/v1/subscriptions", nil, true)
	if !strings.Contains(string(body), `"file":"lista.m3u"`) || strings.Contains(string(body), "panel.example") {
		t.Fatalf("subscriptions: %s", body)
	}
	code, msg := upload(t, e, "PUT", "/api/v1/subscriptions/1/file?file=pagina.m3u", "<html><body>Accedi</body></html>", "en")
	if code != 400 || !strings.Contains(msg, "the file is not an M3U playlist") {
		t.Fatalf("page: %d %s", code, msg)
	}
	if code, body := upload(t, e, "PUT", "/api/v1/subscriptions/1/file?file=nuova.m3u", list[:strings.Index(list, "#EXTINF:-1 tvg-id=\"b\"")], "it"); code != 202 {
		t.Fatalf("replace: %d %s", code, body)
	}
	waitLive := func(e *env, n string) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			_, body := e.do(t, "GET", "/api/v1/subscriptions", nil, true)
			if strings.Contains(string(body), `"live":`+n) && strings.Contains(string(body), `"syncing":false`) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("subscriptions: %s", body)
			}
		}
	}
	waitLive(e, "1")

	code, file := e.do(t, "POST", "/api/v1/backup", map[string]string{"password": "una password lunga"}, true)
	if code != 200 {
		t.Fatalf("backup: %d", code)
	}
	n := setup(t, true)
	if code, body := restore(t, n, file, "una password lunga"); code != 204 {
		t.Fatalf("restore: %d %s", code, body)
	}
	waitLive(n, "1")
}

// An upload refused before its body is read (no name, no such
// subscription) is still read to the end: left unread past 256 KB,
// net/http closes the connection while the browser is still sending, and
// the page shows a failed upload instead of the reason.
func TestPlaylistFileRefusedEarly(t *testing.T) {
	e := setup(t, true)
	big := strings.Repeat("#EXTINF:-1,Canale\nhttp://panel.example/u/p/1.ts\n", 1<<15) // over 1 MB
	for _, c := range []struct {
		method, path, msg string
		code              int
	}{
		{"POST", "/api/v1/subscriptions/file?name=&vpn_id=1&file=lista.m3u", "nome dell'abbonamento obbligatorio", 400},
		{"PUT", "/api/v1/subscriptions/99/file?file=lista.m3u", "", 404},
	} {
		body := strings.NewReader(big)
		req := httptest.NewRequest(c.method, c.path, body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-VodMax-Lang", "it")
		rec := httptest.NewRecorder()
		e.srv.Config.Handler.ServeHTTP(rec, req)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.msg) || body.Len() != 0 {
			t.Errorf("%s %s: %d %s, %d bytes unread", c.method, c.path, rec.Code, rec.Body, body.Len())
		}
	}
}
