package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMovieAndAuth(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Query().Get("api_key")+"|"+r.Header.Get("Authorization"))
		if r.URL.Query().Get("api_key") == "sbagliata" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/configuration":
			w.Write([]byte(`{}`))
		case "/movie/3021":
			if q := r.URL.Query(); q.Get("language") != "it-IT" || !strings.Contains(q.Get("append_to_response"), "images") ||
				!strings.Contains(q.Get("append_to_response"), "external_ids") || q.Get("include_image_language") != "it,en,null" {
				t.Errorf("query: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"title":"1408","original_title":"1408","overview":"Uno scrittore...","poster_path":"/p.jpg","backdrop_path":"/b.jpg",
				"release_date":"2007-06-22","runtime":104,"vote_average":6.9,"genres":[{"name":"Horror"}],
				"credits":{"cast":[{"name":"John Cusack","character":"Mike Enslin","profile_path":"/jc.jpg"},{"name":"Samuel L. Jackson","character":"Olin"}],"crew":[{"name":"X","job":"Producer"},{"name":"Mikael Håfström","job":"Director"}]},
				"videos":{"results":[{"site":"YouTube","type":"Trailer","key":"enKEY","iso_639_1":"en"},{"site":"YouTube","type":"Trailer","key":"itKEY","iso_639_1":"it"}]},
				"images":{"logos":[{"file_path":"/de.png","iso_639_1":"de"},{"file_path":"/en.png","iso_639_1":"en"},{"file_path":"/it.svg","iso_639_1":"it"},{"file_path":"/it.png","iso_639_1":"it"},{"file_path":"/none.png","iso_639_1":null}]},
				"external_ids":{"imdb_id":"tt0450385"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := New("chiave-v3", nil).WithBase(srv.URL)
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := c.Movie(context.Background(), "3021")
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "1408" || m.Poster != srv.URL+"/img/"+PosterSize+"/p.jpg" || m.Director != "Mikael Håfström" || m.Trailer != "itKEY" ||
		len(m.Cast) != 2 || m.Runtime != 104 || m.Genres[0] != "Horror" {
		t.Fatalf("%+v", m)
	}
	if m.Logo != srv.URL+"/img/"+LogoSize+"/it.png" || m.ImdbID != "tt0450385" || len(m.People) != 2 ||
		m.People[0] != (Person{Name: "John Cusack", Role: "Mike Enslin", Photo: srv.URL + "/img/" + ProfileSize + "/jc.jpg"}) ||
		m.People[1].Photo != "" {
		t.Fatalf("logo/people: %+v", m)
	}
	if _, err := c.Movie(context.Background(), "../tv/1"); err == nil {
		t.Fatal("path injection accepted")
	}
	if err := New("sbagliata", nil).WithBase(srv.URL).Check(context.Background()); err != ErrKey {
		t.Fatalf("bad key: %v", err)
	}
	New("eyJhbGciOi.v4token", nil).WithBase(srv.URL).Check(context.Background())
	if last := seen[len(seen)-1]; last != "|Bearer eyJhbGciOi.v4token" {
		t.Fatalf("v4 token sent as %q", last)
	}
	// The key never appears in errors.
	_, err = New("chiave-segreta", nil).WithBase("http://127.0.0.1:1").Movie(context.Background(), "1")
	if err == nil || strings.Contains(err.Error(), "chiave-segreta") {
		t.Fatalf("error leaks the key: %v", err)
	}
}
