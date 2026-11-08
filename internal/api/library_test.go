package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A tiny PNG: enough for the image checks.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func put(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalCollections(t *testing.T) {
	e, _ := tvSetup(t) // provider B has "1408" (2007), also in 4K
	films, series := t.TempDir(), t.TempDir()
	video := make([]byte, 1<<20)
	for i := range video {
		video[i] = byte(i)
	}
	put(t, films, "1408 (2007)/1408.2007.1080p.mp4", video)
	put(t, films, "1408 (2007)/poster.png", pngBytes)
	put(t, films, "1408 (2007)/fanart.png", pngBytes)
	put(t, films, "1408 (2007)/movie.nfo", []byte(`<movie><title>1408</title><year>2007</year><plot>Cobb ruba segreti.</plot><director>Christopher Nolan</director></movie>`))
	put(t, films, "Film di casa.mkv", video[:1000])
	put(t, series, "Serie di casa/Stagione 1/S01E01 Inizio.mkv", video[:1000])
	put(t, series, "Serie di casa/Stagione 1/S01E02 Fine.mkv", video[:1000])

	// Not a folder, not absolute, wrong content: refused.
	for _, bad := range []map[string]any{
		{"name": "X", "kind": "local", "path": "relativa", "content": "movie"},
		{"name": "X", "kind": "local", "path": filepath.Join(films, "manca"), "content": "movie"},
		{"name": "X", "kind": "local", "path": films, "content": "musica"},
	} {
		if code, body := e.do(t, "POST", "/api/v1/subscriptions", bad, true); code != 400 {
			t.Fatalf("raccolta non valida accettata: %v → %d %s", bad, code, body)
		}
	}
	// No VPN needed.
	if code, body := e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "Film di casa", "kind": "local", "path": films, "content": "movie"}, true); code != 201 {
		t.Fatalf("raccolta film: %d %s", code, body)
	}
	if code, body := e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "Serie di casa", "kind": "local", "path": series, "content": "series"}, true); code != 201 {
		t.Fatalf("raccolta serie: %d %s", code, body)
	}
	for _, id := range []string{"2", "3"} {
		if code, body := e.do(t, "POST", "/api/v1/subscriptions/"+id+"/sync?wait=true", nil, true); code != 200 {
			t.Fatalf("sync %s: %d %s", id, code, body)
		}
	}

	// 1408 at home and at the provider (twice): one card, the home file
	// first even if the provider has a 4K copy.
	_, body := e.do(t, "GET", "/api/v1/catalog/movie?q=1408", nil, true)
	var page struct {
		Items []struct {
			ID             int64  `json:"id"`
			SubscriptionID int64  `json:"subscription_id"`
			VariantCount   int    `json:"variant_count"`
			Logo           string `json:"logo"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &page) != nil || len(page.Items) != 1 || page.Items[0].SubscriptionID != 2 || page.Items[0].VariantCount != 3 || page.Items[0].Logo == "" {
		t.Fatalf("1408: %s", body)
	}
	film := strconv.FormatInt(page.Items[0].ID, 10)
	findItem(t, e, "movie", "casa")

	// The poster comes from the folder.
	resp, err := http.Get(e.srv.URL + page.Items[0].Logo)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || len(img) != len(pngBytes) {
		t.Fatalf("locandina: %d %s %d", resp.StatusCode, resp.Header.Get("Content-Type"), len(img))
	}
	// Details from the .nfo, with the folder's backdrop.
	_, body = e.do(t, "GET", "/api/v1/items/"+film+"/details", nil, true)
	if !strings.Contains(string(body), "Cobb ruba segreti") || !strings.Contains(string(body), "Christopher Nolan") || !strings.Contains(string(body), `"backdrop"`) {
		t.Fatalf("dettagli: %s", body)
	}

	// Playback: the file itself, with ranges.
	code, body := e.do(t, "POST", "/api/v1/vod/movie/"+film+"/session", map[string]string{"viewer": "salotto-pc-01", "mode": "direct"}, true)
	var sess struct{ Mode, URL string }
	if code != 200 || json.Unmarshal(body, &sess) != nil || sess.Mode != "direct" {
		t.Fatalf("sessione: %d %s", code, body)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+sess.URL, nil)
	req.Header.Set("Range", "bytes=1000-1099")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || resp.Header.Get("Content-Range") != "bytes 1000-1099/1048576" || len(got) != 100 || got[0] != byte(1000%256) {
		t.Fatalf("intervallo: %d %q %d", resp.StatusCode, resp.Header.Get("Content-Range"), len(got))
	}

	// Series: episodes from the folders, playable.
	_, body = e.do(t, "GET", "/api/v1/catalog/series?q=casa", nil, true)
	json.Unmarshal(body, &page)
	if len(page.Items) != 1 {
		t.Fatalf("serie: %s", body)
	}
	_, body = e.do(t, "GET", "/api/v1/series/"+strconv.FormatInt(page.Items[0].ID, 10)+"/episodes", nil, true)
	var eps []struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	if json.Unmarshal(body, &eps) != nil || len(eps) != 2 || eps[0].Title != "Inizio" {
		t.Fatalf("episodi: %s", body)
	}
	if code, body := e.do(t, "POST", "/api/v1/vod/episode/"+strconv.FormatInt(eps[1].ID, 10)+"/session",
		map[string]string{"viewer": "salotto-pc-01", "mode": "direct"}, true); code != 200 || !strings.Contains(string(body), "S1E2 Fine") {
		t.Fatalf("sessione episodio: %d %s", code, body)
	}

	// A film removed from the folder leaves the catalog at the next scan.
	os.Remove(filepath.Join(films, "Film di casa.mkv"))
	e.do(t, "POST", "/api/v1/subscriptions/2/sync?wait=true", nil, true)
	if _, body := e.do(t, "GET", "/api/v1/catalog/movie?q=casa", nil, true); strings.Contains(string(body), `"id"`) {
		t.Fatalf("film rimosso ancora in catalogo: %s", body)
	}

	// The folder picker.
	_, body = e.do(t, "GET", "/api/v1/folders?path="+films, nil, true)
	if !strings.Contains(string(body), `"name":"1408 (2007)"`) {
		t.Fatalf("cartelle: %s", body)
	}
	if code, _ := e.do(t, "GET", "/api/v1/folders?path=relativa", nil, true); code != 400 {
		t.Fatal("percorso relativo accettato")
	}
}
