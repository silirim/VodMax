package library

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseName(t *testing.T) {
	for _, c := range []struct {
		in      string
		title   string
		year, q int
	}{
		{"Il Padrino (1972)", "Il Padrino", 1972, 2},
		{"The.Godfather.1972.1080p.BluRay.x264-GRP", "The Godfather", 1972, 3},
		{"1917 (2019) [2160p]", "1917", 2019, 4},
		{"Blade Runner 2049 (2017)", "Blade Runner 2049", 2017, 2},
		{"1917", "1917", 0, 2},
		{"Amélie", "Amélie", 0, 2},
		{"Dune Part Two 2024 ITA ENG 2160p WEB-DL", "Dune Part Two", 2024, 4},
	} {
		title, year, q := ParseName(c.in)
		if title != c.title || year != c.year || q != c.q {
			t.Errorf("%q → %q %d %d, atteso %q %d %d", c.in, title, year, q, c.title, c.year, c.q)
		}
	}
}

func TestScanMovies(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Il Padrino (1972)/Il.Padrino.1972.1080p.mkv", "x")
	write(t, root, "Il Padrino (1972)/poster.jpg", "x")
	write(t, root, "Il Padrino (1972)/fanart.jpg", "x")
	write(t, root, "Il Padrino (1972)/movie.nfo", `<?xml version="1.0"?><movie><title>Il padrino</title><year>1972</year>
		<plot>Don Vito Corleone…</plot><ratings><rating name="tmdb" default="true"><value>8.7</value></rating></ratings>
		<genre>Drama</genre><genre>Crime</genre><director>Francis Ford Coppola</director>
		<actor><name>Marlon Brando</name></actor><uniqueid type="tmdb">238</uniqueid></movie>
		https://www.themoviedb.org/movie/238`)
	write(t, root, "Il Padrino (1972)/Sample/sample.mkv", "x")
	write(t, root, "Il Padrino (1972)/Extras/Making of.mkv", "x")
	write(t, root, "Vari/Amélie (2001).mp4", "x")
	write(t, root, "Vari/Amélie (2001)-poster.jpg", "x")
	write(t, root, "Vari/Heat 1995 720p.avi", "x")
	write(t, root, "Inception (2010)/film.mp4", "x")
	write(t, root, "Generici/Ronin.mkv", "x")
	write(t, root, ".nascosto/film.mkv", "x")
	write(t, root, "note.txt", "x")
	res, err := Scan(root, Movies)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 5 {
		t.Fatalf("%d film: %+v", len(res.Items), res.Items)
	}
	byID := map[string]int{}
	for i, it := range res.Items {
		byID[it.SourceID] = i
	}
	p := res.Items[byID["Il Padrino (1972)/Il.Padrino.1972.1080p.mkv"]]
	if p.DisplayTitle != "Il padrino" || p.Year != 1972 || p.Rating != 8.7 || p.TMDBID != "238" || p.Quality != 3 ||
		p.Image != "local:Il Padrino (1972)/poster.jpg" || p.Extension != "mkv" || p.GroupKey != "il padrino|1972" {
		t.Errorf("Il Padrino: %+v", p)
	}
	a := res.Items[byID["Vari/Amélie (2001).mp4"]]
	if a.DisplayTitle != "Amélie" || a.Year != 2001 || a.Image != "local:Vari/Amélie (2001)-poster.jpg" {
		t.Errorf("Amélie: %+v", a)
	}
	if h := res.Items[byID["Vari/Heat 1995 720p.avi"]]; h.DisplayTitle != "Heat" || h.Year != 1995 || h.Image != "" {
		t.Errorf("Heat: %+v", h)
	}
	if it := res.Items[byID["Inception (2010)/film.mp4"]]; it.DisplayTitle != "Inception" || it.Year != 2010 {
		t.Errorf("titolo dalla cartella: %+v", it)
	}
	if it := res.Items[byID["Generici/Ronin.mkv"]]; it.DisplayTitle != "Ronin" {
		t.Errorf("cartella generica: %+v", it)
	}
	n, fanart := Details(root, "movie", "Il Padrino (1972)/Il.Padrino.1972.1080p.mkv")
	if n.Plot == "" || len(n.Actors) != 1 || n.Directors[0] != "Francis Ford Coppola" || fanart != "Il Padrino (1972)/fanart.jpg" {
		t.Errorf("dettagli: %+v %q", n, fanart)
	}
	// In a shared folder a film does not take the folder's fanart.
	if _, fanart := Details(root, "movie", "Vari/Heat 1995 720p.avi"); fanart != "" {
		t.Errorf("fanart di Heat: %q", fanart)
	}
}

func TestScanSeries(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Twin Peaks (1990)/tvshow.nfo", `<tvshow><title>I segreti di Twin Peaks</title><plot>Laura Palmer…</plot></tvshow>`)
	write(t, root, "Twin Peaks (1990)/poster.jpg", "x")
	write(t, root, "Twin Peaks (1990)/Stagione 1/Twin.Peaks.S01E01.Pilot.1080p.mkv", "x")
	write(t, root, "Twin Peaks (1990)/Stagione 1/Twin Peaks S01E02 - Traces to Nowhere.mkv", "x")
	write(t, root, "Twin Peaks (1990)/Stagione 1/Twin Peaks S01E02 - Traces to Nowhere-thumb.jpg", "x")
	write(t, root, "Twin Peaks (1990)/Stagione 1/Twin Peaks S01E02 - Traces to Nowhere.nfo",
		`<episodedetails><title>Tracce verso il nulla</title><plot>Cooper…</plot><runtime>47</runtime></episodedetails>`)
	write(t, root, "Twin Peaks (1990)/Stagione 2/2x01 Possano i giganti.mkv", "x")
	write(t, root, "Mini/Season 1/Episodio 2.mp4", "x")
	write(t, root, "Mini/Season 1/finale.mp4", "x")
	write(t, root, "Vuota/leggimi.txt", "x")
	write(t, root, "Zagubieni/Sezon 2/Odcinek 3.mkv", "x")
	res, err := Scan(root, Series)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("%d serie: %+v", len(res.Items), res.Items)
	}
	tp := res.Items[1]
	if tp.SourceID != "Twin Peaks (1990)" || tp.DisplayTitle != "I segreti di Twin Peaks" || tp.Year != 1990 ||
		tp.Image != "local:Twin Peaks (1990)/poster.jpg" || tp.Quality != 2 {
		t.Errorf("serie: %+v", tp)
	}
	eps := res.Episodes["Twin Peaks (1990)"]
	want := []struct {
		s, n  int
		title string
	}{{1, 1, "Pilot"}, {1, 2, "Tracce verso il nulla"}, {2, 1, "Possano i giganti"}}
	if len(eps) != len(want) {
		t.Fatalf("episodi: %+v", eps)
	}
	for i, w := range want {
		if eps[i].Season != w.s || eps[i].Number != w.n || eps[i].Title != w.title {
			t.Errorf("episodio %d: %+v, atteso %+v", i, eps[i], w)
		}
	}
	if eps[1].Image != "local:Twin Peaks (1990)/Stagione 1/Twin Peaks S01E02 - Traces to Nowhere-thumb.jpg" || eps[0].Image != "" {
		t.Errorf("miniature: %q %q", eps[0].Image, eps[1].Image)
	}
	if eps[1].Duration != 47*60 || eps[1].Plot == "" {
		t.Errorf("episodio dal .nfo: %+v", eps[1])
	}
	mini := res.Episodes["Mini"]
	if len(mini) != 2 || mini[0].Number != 1 || mini[0].Title != "finale" || mini[1].Number != 2 || mini[1].Title != "Episodio 2" {
		t.Errorf("Mini: %+v", mini)
	}
	if z := res.Episodes["Zagubieni"]; len(z) != 1 || z[0].Season != 2 || z[0].Number != 3 {
		t.Errorf("Zagubieni: %+v", z)
	}
}

func TestResolve(t *testing.T) {
	for _, bad := range []string{"", "/", "..", "../.."} {
		if f, err := Resolve("/coll", bad); err == nil && f != "/coll" {
			if f == "/" || !filepathHasPrefix(f, "/coll") {
				t.Errorf("%q → %q", bad, f)
			}
		}
	}
	if f, _ := Resolve("/coll", "../etc/passwd"); f != "/coll/etc/passwd" {
		t.Errorf("uscita dalla cartella: %q", f)
	}
}

func filepathHasPrefix(p, prefix string) bool {
	rel, err := filepath.Rel(prefix, p)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && (len(rel) < 2 || rel[:2] != "..")
}
