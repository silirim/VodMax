// Package web serves the VodMax user interface.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; media-src 'self' blob:; "+
			"script-src 'self'; style-src 'self'; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".html") {
			h.Set("Cache-Control", "no-store")
		}
		if r.URL.Path == "/i18n/languages.js" {
			h.Set("Content-Type", "text/javascript; charset=utf-8")
			h.Set("Cache-Control", "no-cache")
			w.Write(langs)
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
