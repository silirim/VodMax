package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Decision 0039: "only through a VPN" is the default and can be switched
// off; then a subscription may have no VPN and uses the normal network.
// Switched on again, that subscription waits: not one request reaches
// the provider.
func TestProviderNetwork(t *testing.T) {
	p := newTVProvider()
	defer p.Close()
	var hits atomic.Int64
	prov := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		p.Config.Handler.ServeHTTP(w, r)
	}))
	defer prov.Close()
	e := setup(t, true)
	sub := map[string]any{"name": "B", "kind": "xtream", "vpn_id": 0, "server": prov.URL, "username": "u", "password": "p"}

	if code, body := e.do(t, "GET", "/api/v1/settings/network", nil, true); code != 200 || !strings.Contains(string(body), `"vpn_only":true`) {
		t.Fatalf("default: %d %s", code, body)
	}
	if code, body := e.do(t, "POST", "/api/v1/subscriptions", sub, true); code != 400 || !strings.Contains(string(body), "VPN") {
		t.Fatalf("no VPN while VPN only: %d %s", code, body)
	}
	if code, body := e.do(t, "PUT", "/api/v1/settings/network", map[string]bool{"vpn_only": false}, true); code != 200 {
		t.Fatalf("switch off: %d %s", code, body)
	}
	code, body := e.do(t, "POST", "/api/v1/subscriptions", sub, true)
	var created struct{ ID int64 }
	if code != 201 || json.Unmarshal(body, &created) != nil || created.ID == 0 {
		t.Fatalf("no VPN allowed: %d %s", code, body)
	}
	if code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync?wait=true", nil, true); code != 200 || !strings.Contains(string(body), `"programmes":4`) || hits.Load() == 0 {
		t.Fatalf("sync on the normal network: %d %s (%d requests)", code, body, hits.Load())
	}

	// On again: the subscription without a VPN is not contacted.
	e.do(t, "PUT", "/api/v1/settings/network", map[string]bool{"vpn_only": true}, true)
	before := hits.Load()
	if code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync?wait=true", nil, true); code == 200 || !strings.Contains(string(body), "VPN") || hits.Load() != before {
		t.Fatalf("VPN only again: %d %s (%d requests)", code, body, hits.Load()-before)
	}
	if code, body := e.do(t, "GET", "/api/v1/tv/channels", nil, true); code != 200 {
		t.Fatalf("catalog kept: %d %s", code, body)
	}

	// A VPN chosen, then taken away: only when allowed.
	e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN", "config": wgConf(t)}, true)
	if code, body := e.do(t, "PATCH", "/api/v1/subscriptions/1", map[string]any{"vpn_id": 1}, true); code != 200 && code != 204 {
		t.Fatalf("choose a VPN: %d %s", code, body)
	}
	if code, body := e.do(t, "PATCH", "/api/v1/subscriptions/1", map[string]any{"vpn_id": 0}, true); code != 400 {
		t.Fatalf("remove the VPN while VPN only: %d %s", code, body)
	}
	e.do(t, "PUT", "/api/v1/settings/network", map[string]bool{"vpn_only": false}, true)
	if code, body := e.do(t, "PATCH", "/api/v1/subscriptions/1", map[string]any{"vpn_id": 0}, true); code != 200 && code != 204 {
		t.Fatalf("remove the VPN: %d %s", code, body)
	}
	if _, body := e.do(t, "GET", "/api/v1/subscriptions", nil, true); !strings.Contains(string(body), `"vpn_id":0`) {
		t.Fatalf("no VPN stored: %s", body)
	}
	if code, body := e.do(t, "PUT", "/api/v1/settings/network", map[string]bool{"vpn_only": true}, false); code != 401 && code != 403 {
		t.Fatalf("admins only: %d %s", code, body)
	}
}
