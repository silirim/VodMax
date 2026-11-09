package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A fake GitHub: two releases (the newer one first is not assumed), with
// checksums; the API must offer the newest and the hashes of its files.
func TestUpdateCheck(t *testing.T) {
	var calls atomic.Int32
	var gh *httptest.Server
	gh = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/repos/silirim/VodMax/releases":
			w.Write([]byte(`[
			 {"tag_name":"v0.3.1","html_url":"https://example.invalid/r/v0.3.1","body":"vecchia","assets":[]},
			 {"tag_name":"v0.4.0","html_url":"https://example.invalid/r/v0.4.0","body":"Novità","published_at":"2026-11-01T10:00:00Z","assets":[
			   {"name":"VodMax.apk","browser_download_url":"` + gh.URL + `/dl/VodMax.apk","size":123},
			   {"name":"vodmax-server_0.4.0_linux_amd64.tar.gz","browser_download_url":"` + gh.URL + `/dl/s-amd64","size":45},
			   {"name":"vodmax-server_0.4.0_linux_arm64.tar.gz","browser_download_url":"` + gh.URL + `/dl/s-arm64","size":46},
			   {"name":"SHA256SUMS","browser_download_url":"` + gh.URL + `/dl/SHA256SUMS","size":300}]},
			 {"tag_name":"v0.5.0","draft":true,"assets":[]},
			 {"tag_name":"v0.5.0-beta.1","prerelease":true,"html_url":"https://example.invalid/r/beta","body":"Beta","assets":[]}]`))
		case "/dl/SHA256SUMS":
			w.Write([]byte(strings.Repeat("a", 64) + "  VodMax.apk\n" + strings.Repeat("b", 64) + "  vodmax-server_0.4.0_linux_amd64.tar.gz\n" +
				strings.Repeat("c", 64) + "  vodmax-server_0.4.0_linux_arm64.tar.gz\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	e := setup(t, true)
	e.svc.SetUpdateAPI(gh.URL)
	e.svc.SetVersion("v0.3.1")

	code, body := e.do(t, "POST", "/api/v1/update/check", nil, true)
	var info struct {
		Enabled, Available bool
		Current, Latest    string
		Notes              string
		APK                *struct{ URL, SHA256 string }
		Server             *struct{ Name, SHA256 string }
	}
	if code != 200 || json.Unmarshal(body, &info) != nil {
		t.Fatalf("check: %d %s", code, body)
	}
	if !info.Available || info.Latest != "v0.4.0" || info.Notes != "Novità" || !info.Enabled ||
		info.APK == nil || info.APK.SHA256 != strings.Repeat("a", 64) || !strings.HasSuffix(info.APK.URL, "/dl/VodMax.apk") {
		t.Fatalf("update: %s", body)
	}
	// The result is kept: devices read it without asking GitHub.
	before := calls.Load()
	if code, body := e.do(t, "GET", "/api/v1/update", nil, true); code != 200 || !strings.Contains(string(body), `"latest":"v0.4.0"`) {
		t.Fatalf("get: %d %s", code, body)
	}
	if calls.Load() != before {
		t.Fatal("GET /update contacted GitHub")
	}
	// Up to date: nothing available.
	e.svc.SetVersion("v0.4.0-3-gabcdef0")
	if _, body := e.do(t, "GET", "/api/v1/update", nil, true); !strings.Contains(string(body), `"available":false`) {
		t.Fatalf("up to date: %s", body)
	}
	// Running a beta is not asking for betas: without the option the
	// beta is not offered, and neither is the older stable version.
	e.svc.SetVersion("v0.4.1-beta.1")
	if _, body := e.do(t, "POST", "/api/v1/update/check", nil, true); !strings.Contains(string(body), `"latest":"v0.4.0"`) ||
		!strings.Contains(string(body), `"available":false`) {
		t.Fatalf("beta without the option: %s", body)
	}
	// With the option on, betas are offered (and checked at once).
	code, body = e.do(t, "PUT", "/api/v1/settings/updates", map[string]bool{"enabled": true, "beta": true}, true)
	if code != 200 || !strings.Contains(string(body), `"latest":"v0.5.0-beta.1"`) || !strings.Contains(string(body), `"beta":true`) ||
		!strings.Contains(string(body), `"prerelease":true`) || !strings.Contains(string(body), `"available":true`) {
		t.Fatalf("beta with the option: %d %s", code, body)
	}
	// Also for whoever runs a stable version.
	e.svc.SetVersion("v0.4.0")
	if _, body := e.do(t, "GET", "/api/v1/update", nil, true); !strings.Contains(string(body), `"available":true`) {
		t.Fatalf("beta for a stable: %s", body)
	}
	// Off again: back to the stable version, the option is kept with the check.
	e.do(t, "PUT", "/api/v1/settings/updates", map[string]bool{"enabled": true, "beta": false}, true)
	if _, body := e.do(t, "GET", "/api/v1/update", nil, true); !strings.Contains(string(body), `"latest":"v0.4.0"`) {
		t.Fatalf("beta off: %s", body)
	}
	// Turned off.
	if code, body := e.do(t, "PUT", "/api/v1/settings/updates", map[string]bool{"enabled": false}, true); code != 200 || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("disable: %d %s", code, body)
	}
}
