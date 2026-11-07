// Package web serves the VodMax user interface.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

//go:embed static
var static embed.FS

// The font: a container without /etc/mime.types would serve it as a plain
// octet stream, and the pages preload it as font/woff2.
func init() { mime.AddExtensionType(".woff2", "font/woff2") }

// Handler serves the interface. The content security policy allows no
// external resource: the interface must never reach a provider (or
// anything else) directly, only the VodMax server.
func Handler() http.Handler {
	root, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(root))
	langs, err := languagesScript(root)
	if err != nil {
		panic(err)
	}
	tags, err := contentTags(root, langs)
	if err != nil {
		panic(err)
	}
	pages, err := taggedPages(root, tags)
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; media-src 'self' blob:; "+
			"script-src 'self'; style-src 'self'; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		name := strings.TrimPrefix(r.URL.Path, "/")
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
			h.Set("Cache-Control", "no-store")
			if r.URL.Path == "/" {
				name = "index.html"
			}
			// /index.html itself still redirects to / (http.FileServer).
			if page, ok := pages[name]; ok && r.URL.Path != "/index.html" {
				h.Set("Content-Type", "text/html; charset=utf-8")
				w.Write(page)
				return
			}
		}
		// The embedded files carry no dates, so without a tag a cache could
		// only keep a file or fetch it again blindly: each one must be checked
		// every time (a 304 when unchanged).
		if tag, ok := tags[name]; ok {
			h.Set("Cache-Control", "no-cache")
			h.Set("ETag", tag)
		}
		if r.URL.Path == "/i18n/languages.js" {
			h.Set("Content-Type", "text/javascript; charset=utf-8")
			http.ServeContent(w, r, "languages.js", time.Time{}, bytes.NewReader(langs))
			return
		}
		files.ServeHTTP(w, r)
	})
}

// languagesScript gathers the dictionaries i18n/<lang>.json (the Italian
// one is the base: its texts are the keys) into one script, so every page
// knows all the languages before i18n.js runs.
func languagesScript(root fs.FS) ([]byte, error) {
	files, err := fs.Glob(root, "i18n/*.json")
	if err != nil {
		return nil, err
	}
	dicts := map[string]map[string]string{}
	for _, f := range files {
		code := strings.TrimSuffix(path.Base(f), ".json")
		if code == "it" {
			continue
		}
		raw, err := fs.ReadFile(root, f)
		if err != nil {
			return nil, err
		}
		d := map[string]string{}
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for k, v := range d {
			if v == "" { // not translated yet (Weblate leaves it empty)
				delete(d, k)
			}
		}
		dicts[code] = d
	}
	raw, err := json.Marshal(dicts)
	if err != nil {
		return nil, err
	}
	return append(append([]byte("window.VodMaxDicts = "), raw...), ";\n"...), nil
}

// contentTags gives every embedded file (and the generated languages.js)
// an ETag made from its content.
func contentTags(root fs.FS, langs []byte) (map[string]string, error) {
	tag := func(b []byte) string { sum := sha256.Sum256(b); return `"` + hex.EncodeToString(sum[:8]) + `"` }
	tags := map[string]string{"i18n/languages.js": tag(langs)}
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(root, p)
		tags[p] = tag(b)
		return err
	})
	return tags, err
}

// assetRef is a page's local script or style ("ui.css", "i18n/languages.js").
var assetRef = regexp.MustCompile(`((?:src|href)=")([^"/:][^":?]*\.(?:css|js))"`)

// taggedPages are the pages with their scripts and styles named by content
// (ui.css?v=…): a proxy that keeps files for hours whatever the server says
// (Cloudflare gives CSS and JS its own browser lifetime) still hands out a
// new version on the day of an update, since the pages are never cached.
func taggedPages(root fs.FS, tags map[string]string) (map[string][]byte, error) {
	names, err := fs.Glob(root, "*.html")
	if err != nil {
		return nil, err
	}
	pages := map[string][]byte{}
	for _, n := range names {
		b, err := fs.ReadFile(root, n)
		if err != nil {
			return nil, err
		}
		pages[n] = assetRef.ReplaceAllFunc(b, func(m []byte) []byte {
			s := assetRef.FindSubmatch(m)
			tag, ok := tags[string(s[2])]
			if !ok {
				return m
			}
			return []byte(string(s[1]) + string(s[2]) + "?v=" + strings.Trim(tag, `"`) + `"`)
		})
	}
	return pages, nil
}
