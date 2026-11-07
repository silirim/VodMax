package web

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func dictionary(t *testing.T, code string) map[string]string {
	t.Helper()
	raw, err := fs.ReadFile(static, "static/i18n/"+code+".json")
	if err != nil {
		t.Fatal(err)
	}
	d := map[string]string{}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("%s.json: %v", code, err)
	}
	return d
}

// keyRE finds t("…") and t("…" + "…") in the pages' scripts.
var keyRE = regexp.MustCompile(`\bt\(\s*((?:"(?:[^"\\]|\\.)*"\s*\+\s*)*"(?:[^"\\]|\\.)*")`)
var partRE = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// htmlTextRE finds an element with data-i18n: its attributes and its text
// up to the first tag.
var htmlTextRE = regexp.MustCompile(`<[a-z0-9]+(\s[^>]*\bdata-i18n\b[^>]*)>([^<]*)`)
var attrRE = regexp.MustCompile(`data-i18n-attr="([^"]*)"`)

// Every text of the interface is in the Italian base (what translators on
// Weblate see) and translated into English.
func TestEveryTextTranslated(t *testing.T) {
	it, en := dictionary(t, "it"), dictionary(t, "en")
	for k, v := range it {
		if k != v {
			t.Errorf("it.json: %q has the value %q", k, v)
		}
	}
	scripts, _ := fs.Glob(static, "static/*.js")
	for _, f := range scripts {
		if f == "static/i18n.js" {
			continue // the loader: its comments show t() as an example
		}
		src, _ := fs.ReadFile(static, f)
		for _, m := range keyRE.FindAllStringSubmatch(string(src), -1) {
			key := ""
			for _, p := range partRE.FindAllStringSubmatch(m[1], -1) {
				s, err := strconv.Unquote(`"` + p[1] + `"`)
				if err != nil {
					t.Fatalf("%s: %s", f, p[1])
				}
				key += s
			}
			if _, ok := it[key]; !ok {
				t.Errorf("%s: %q missing in it.json", f, key)
			}
			if en[key] == "" {
				t.Errorf("%s: %q not translated in en.json", f, key)
			}
		}
	}
	// Static text of the pages: data-i18n elements and their attributes.
	pages, _ := fs.Glob(static, "static/*.html")
	for _, f := range pages {
		src, _ := fs.ReadFile(static, f)
		for _, m := range htmlTextRE.FindAllStringSubmatch(string(src), -1) {
			texts := []string{strings.TrimSpace(m[2])}
			if a := attrRE.FindStringSubmatch(m[1]); a != nil {
				for _, name := range strings.Split(a[1], ",") {
					if v := regexp.MustCompile(`\s` + strings.TrimSpace(name) + `="([^"]*)"`).FindStringSubmatch(m[1]); v != nil {
						texts = append(texts, v[1])
					}
				}
			}
			for _, text := range texts {
				text = strings.NewReplacer("&amp;", "&", "&#39;", "'", "&quot;", `"`).Replace(text)
				if text != "" && en[text] == "" && text != "Multiview" && text != "Menu" {
					t.Errorf("%s: %q not translated in en.json", f, text)
				}
			}
		}
	}
	for k := range en {
		if _, ok := it[k]; !ok {
			t.Errorf("en.json: %q is not in it.json", k)
		}
	}
}

func TestLanguagesScript(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/i18n/languages.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body strings.Builder
	buf := make([]byte, 64<<10)
	for {
		n, err := resp.Body.Read(buf)
		body.Write(buf[:n])
		if err != nil {
			break
		}
	}
	js, ok := strings.CutPrefix(body.String(), "window.VodMaxDicts = ")
	if resp.StatusCode != 200 || !ok || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("%d %s %.60s", resp.StatusCode, resp.Header.Get("Content-Type"), body.String())
	}
	var dicts map[string]map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimSpace(js), ";")), &dicts); err != nil {
		t.Fatal(err)
	}
	if _, it := dicts["it"]; it || dicts["en"]["Continua"] != "Continue" {
		t.Fatalf("languages: %d, it present: %v", len(dicts), it)
	}
}

// The pages name their scripts and styles by content and every file must be
// revalidated, so an update reaches browsers behind a caching proxy at once.
func TestStaticFilesRevalidated(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(p, etag string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", srv.URL+p, nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	resp, page := get("/", "")
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("/: Cache-Control %q", resp.Header.Get("Cache-Control"))
	}
	m := regexp.MustCompile(`href="ui\.css\?v=([0-9a-f]{16})"`).FindStringSubmatch(page)
	if m == nil || !strings.Contains(page, `src="i18n/languages.js?v=`) {
		t.Fatalf("the page does not name its files by content:\n%s", page[:min(len(page), 600)])
	}
	for _, p := range []string{"/ui.css?v=" + m[1], "/i18n/languages.js"} {
		resp, _ := get(p, "")
		tag := resp.Header.Get("ETag")
		if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-cache" || tag == "" {
			t.Fatalf("%s: %d, Cache-Control %q, ETag %q", p, resp.StatusCode, resp.Header.Get("Cache-Control"), tag)
		}
		if strings.HasPrefix(p, "/ui.css") && tag != `"`+m[1]+`"` {
			t.Errorf("ui.css: ETag %s, page names v=%s", tag, m[1])
		}
		if resp, _ := get(p, tag); resp.StatusCode != http.StatusNotModified {
			t.Errorf("%s with its ETag: %d, want 304", p, resp.StatusCode)
		}
	}
	if resp, _ := get("/index.html", ""); resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("/index.html: %d, want the redirect to /", resp.StatusCode)
	}
}
