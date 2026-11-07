package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func fakeTMDB(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/img/") {
			w.Write(append([]byte("\xff\xd8\xff\xe0"), make([]byte, 64)...)) // JPEG
			return
		}
		calls.Add(1)
		if r.URL.Query().Get("api_key") != "chiave-buona" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/configuration":
			w.Write([]byte(`{}`))
		case "/movie/539":
			w.Write([]byte(`{"title":"Psyco","original_title":"Psycho","overview":"Marion Crane fugge...","poster_path":"/psycho.jpg",
				"backdrop_path":"/psycho-b.jpg","release_date":"1960-06-16","runtime":109,"vote_average":8.4,"genres":[{"name":"Horror"}],
				"credits":{"cast":[{"name":"Anthony Perkins","character":"Norman Bates","profile_path":"/perkins.jpg"},{"name":"Janet Leigh","character":"Marion Crane"}]},
				"images":{"logos":[{"file_path":"/psycho-en.png","iso_639_1":"en"}]}}`))
		case "/trending/movie/week":
			if r.URL.Query().Get("page") == "1" {
				w.Write([]byte(`{"results":[{"id":539,"title":"Psyco","original_title":"Psycho","release_date":"1960-06-16"},
					{"id":777,"title":"Non nel catalogo","release_date":"2026-01-01"}]}`))
			} else {
				w.Write([]byte(`{"results":[]}`))
			}
		case "/trending/tv/week":
			if r.URL.Query().Get("page") == "1" {
				w.Write([]byte(`{"results":[{"id":1920,"name":"I segreti di Twin Peaks","original_name":"Twin Peaks","first_air_date":"1990-04-08"}]}`))
			} else {
				w.Write([]byte(`{"results":[]}`))
			}
		case "/movie/3021":
			w.Write([]byte(`{"title":"1408","overview":"Trama TMDB","poster_path":"/1408.jpg"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func get(t *testing.T, e *env, path string) (int, string, []byte) {
	t.Helper()
	resp, err := http.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

type details struct {
	Title, Plot, Cast, Director, Trailer, Backdrop, Logo string
	People                                               []struct{ Name, Role, Photo string }
	Sources                                              []string
	Duration                                             int
}

func readDetails(t *testing.T, e *env, id string) details {
	t.Helper()
	code, body := e.do(t, "GET", "/api/v1/items/"+id+"/details", nil, true)
	var d details
	if code != 200 || json.Unmarshal(body, &d) != nil {
		t.Fatalf("details: %d %s", code, body)
	}
	return d
}

// "Titoli simili" comes from the catalogue alone: same kind, shared
// categories through any version, never the title itself or its other
// versions, and hidden to kids like the title.
func TestSimilarTitles(t *testing.T) {
	e, _ := tvSetup(t)
	film := strconv.FormatInt(findItem(t, e, "movie", "1408"), 10)
	psycho := findItem(t, e, "movie", "psycho")
	series := strconv.FormatInt(findItem(t, e, "series", "twin"), 10)
	var list []struct {
		ID    int64
		Title string `json:"display_title"`
	}
	// Only one version of 1408 shares Horror with Psycho, whichever one
	// the catalogue shows as primary; that is enough.
	code, body := e.do(t, "GET", "/api/v1/items/"+film+"/similar", nil, true)
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list) != 1 || list[0].ID != psycho || list[0].Title != "Psycho" {
		t.Fatalf("similar to 1408: %d %s", code, body)
	}
	if code, body := e.do(t, "GET", "/api/v1/items/"+series+"/similar", nil, true); code != 200 || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("lone series: %d %s", code, body)
	}
	// Kids: an adult title has no page, hence no row either.
	e.do(t, "PUT", "/api/v1/subscriptions/1/categories/movie/10", map[string]any{"adult": true}, true)
	e.do(t, "PUT", "/api/v1/subscriptions/1/categories/movie/11", map[string]any{"adult": true}, true)
	e.do(t, "PUT", "/api/v1/subscriptions/1/rules", map[string]any{"adult": true}, true)
	kid := addKids(t, e)
	if code, _ := e.call(t, "GET", "/api/v1/items/"+film+"/similar", nil, kid.device, kid.profile); code != 404 {
		t.Fatalf("adult title's row shown to kids: %d", code)
	}
	if code, body := e.call(t, "GET", "/api/v1/items/"+series+"/similar", nil, kid.device, kid.profile); code != 200 {
		t.Fatalf("kids similar: %d %s", code, body)
	}
}

func TestDetailsAndTMDBFallback(t *testing.T) {
	e, _ := tvSetup(t)
	film := strconv.FormatInt(findItem(t, e, "movie", "1408"), 10)
	psycho := strconv.FormatInt(findItem(t, e, "movie", "psycho"), 10)

	// Provider details, through the VPN.
	d := readDetails(t, e, film)
	if d.Plot != "Uno scrittore scettico..." || d.Director != "Mikael Håfström" || d.Trailer != "abcDEF12345" || d.Duration != 6240 ||
		d.Backdrop == "" || len(d.Sources) != 1 || d.Sources[0] != "provider" {
		t.Fatalf("provider details: %+v", d)
	}
	if code, ctype, _ := get(t, e, d.Backdrop); code != 200 || ctype != "image/png" {
		t.Fatalf("backdrop: %d %s", code, ctype)
	}
	if code, _, _ := get(t, e, strings.Replace(d.Backdrop, "?s=", "?s=00", 1)); code != 403 {
		t.Fatalf("forged backdrop signature: %d", code)
	}
	// No details, no image, no TMDB: nothing invented.
	if d := readDetails(t, e, psycho); d.Plot != "" || d.Backdrop != "" || len(d.Sources) != 0 {
		t.Fatalf("empty details: %+v", d)
	}
	_, body := e.do(t, "GET", "/api/v1/catalog/movie?q=psycho", nil, true)
	if strings.Contains(string(body), `"logo"`) {
		t.Fatalf("logo without image and without TMDB: %s", body)
	}

	// TMDB with the user's key.
	tm, calls := fakeTMDB(t)
	e.svc.SetTMDBBase(tm.URL)
	if code, _ := e.do(t, "PUT", "/api/v1/settings/metadata", map[string]string{"tmdb_key": "sbagliata"}, true); code != 400 {
		t.Fatalf("bad key accepted: %d", code)
	}
	if code, body := e.do(t, "PUT", "/api/v1/settings/metadata", map[string]string{"tmdb_key": "chiave-buona"}, true); code != 200 || !strings.Contains(string(body), `"tmdb":true`) {
		t.Fatalf("key: %d %s", code, body)
	}
	if _, body := e.do(t, "GET", "/api/v1/settings/metadata", nil, true); strings.Contains(string(body), "chiave-buona") {
		t.Fatal("key returned")
	}
	// Psycho now has the TMDB poster and an Italian overview.
	_, body = e.do(t, "GET", "/api/v1/catalog/movie?q=psycho", nil, true)
	var page struct {
		Items []struct{ Logo string }
	}
	json.Unmarshal(body, &page)
	if len(page.Items) != 1 || page.Items[0].Logo == "" {
		t.Fatalf("no TMDB poster: %s", body)
	}
	if code, ctype, _ := get(t, e, page.Items[0].Logo); code != 200 || ctype != "image/jpeg" {
		t.Fatalf("TMDB poster: %d %s", code, ctype)
	}
	d = readDetails(t, e, psycho)
	if d.Title != "Psyco" || d.Plot != "Marion Crane fugge..." || d.Backdrop == "" || d.Duration != 109*60 {
		t.Fatalf("TMDB details: %+v", d)
	}
	if code, ctype, _ := get(t, e, d.Backdrop); code != 200 || ctype != "image/jpeg" {
		t.Fatalf("TMDB backdrop: %d %s", code, ctype)
	}
	// Title logo and cast photos, through the server like the backdrop
	// (the page's CSP allows no other image host).
	if d.Logo == "" || len(d.People) != 2 || d.People[0].Name != "Anthony Perkins" || d.People[0].Role != "Norman Bates" ||
		d.People[0].Photo == "" || d.People[1].Photo != "" {
		t.Fatalf("logo and people: %+v", d)
	}
	for _, u := range []string{d.Logo, d.People[0].Photo} {
		if code, ctype, _ := get(t, e, u); code != 200 || ctype != "image/jpeg" {
			t.Fatalf("meta image %s: %d %s", u, code, ctype)
		}
		if code, _, _ := get(t, e, strings.Replace(u, "?s=", "?s=00", 1)); code != 403 {
			t.Fatalf("forged signature accepted: %s", u)
		}
	}
	// The provider's plot wins (it is the one in Italian for this panel).
	if d := readDetails(t, e, film); d.Plot != "Uno scrittore scettico..." || len(d.Sources) != 2 {
		t.Fatalf("merged details: %+v", d)
	}
	// Trending: the week's titles the catalog has, films and series
	// alternating (by TMDB id, or by the original title).
	_, body = e.do(t, "GET", "/api/v1/trending", nil, true)
	var trend []struct {
		Title string `json:"display_title"`
		Kind  string
	}
	if json.Unmarshal(body, &trend) != nil || len(trend) != 2 || trend[0].Title != "Psycho" || trend[1].Title != "Twin Peaks" {
		t.Fatalf("trending: %s", body)
	}
	// Cached: no new TMDB calls.
	before := calls.Load()
	readDetails(t, e, psycho)
	readDetails(t, e, film)
	if calls.Load() != before {
		t.Fatalf("TMDB called again: %d → %d", before, calls.Load())
	}

	// The key travels in the password backup to a new installation.
	_, file := e.do(t, "POST", "/api/v1/backup", map[string]string{"password": "password lunga"}, true)
	if strings.Contains(string(file), "chiave-buona") {
		t.Fatal("key in clear in the backup file")
	}
	n := setup(t, true)
	n.svc.SetTMDBBase(tm.URL)
	if code, _ := restore(t, n, file, "password lunga"); code != 204 {
		t.Fatalf("restore: %d", code)
	}
	if _, body := n.do(t, "GET", "/api/v1/settings/metadata", nil, true); !strings.Contains(string(body), `"tmdb":true`) {
		t.Fatalf("key not restored: %s", body)
	}
}
