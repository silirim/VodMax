package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/service"
	"github.com/silirim/vodmax/internal/store"
	"github.com/silirim/vodmax/internal/vpn"
)

// Fixture credentials are generated per run: no literal secrets in tests.
var (
	token    = randomHex(16)
	password = "pw-" + randomHex(6)
)

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// fakeTunnel stands in for the WireGuard tunnel: it dials the normal
// network so the test can reach an httptest provider.
type fakeTunnel struct{ up atomic.Bool }

func (f *fakeTunnel) Up() bool           { return f.up.Load() }
func (f *fakeTunnel) Status() vpn.Status { return vpn.Status{Connected: f.up.Load()} }
func (f *fakeTunnel) Close()             { f.up.Store(false) }
func (f *fakeTunnel) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func fakeProvider(hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		q := r.URL.Query()
		if q.Get("password") != password {
			w.Write([]byte(`{"user_info":{"auth":0}}`))
			return
		}
		switch q.Get("action") {
		case "":
			w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","exp_date":"1893456000","max_connections":"1","active_cons":"0"}}`))
		case "get_live_categories":
			w.Write([]byte(`[{"category_id":"1","category_name":"Italia"}]`))
		case "get_vod_categories":
			w.Write([]byte(`[{"category_id":"10","category_name":"Horror"}]`))
		case "get_series_categories":
			w.Write([]byte(`[{"category_id":"20","category_name":"Drama"}]`))
		case "get_live_streams":
			w.Write([]byte(`[{"num":1,"name":"Rai 1 HD","stream_id":101,"category_id":"1","epg_channel_id":"rai1.it","tv_archive":1,"tv_archive_duration":"7"}]`))
		case "get_vod_streams":
			w.Write([]byte(`[{"name":"1408 (2007) 4K","stream_id":555,"category_id":"10","container_extension":"mkv","rating":"7.1"},
				{"name":"Inception","stream_id":556,"category_id":"10","year":"2010"}]`))
		case "get_series":
			w.Write([]byte(`[{"name":"Twin Peaks","series_id":77,"category_id":"20","releaseDate":"1990-04-08"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
}

type env struct {
	svc        *service.Service
	srv        *httptest.Server
	tunnel     *fakeTunnel
	dbPath     string
	recordings string
	stop       func() // stops the server like a restart (see restart)
	start      func() // starts it again on the same data
}

func setup(t *testing.T, tunnelComesUp bool) *env {
	t.Helper()
	dir := t.TempDir()
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{tunnel: &fakeTunnel{}, dbPath: filepath.Join(dir, "vodmax.db")}
	e.recordings = filepath.Join(dir, "recordings")
	e.start = func() {
		st, err := store.Open(e.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		svc := service.New(st, box, func(ctx context.Context, cfg *vpn.Config) (service.Tunnel, error) {
			e.tunnel.up.Store(tunnelComesUp)
			return e.tunnel, nil
		})
		e.svc = svc
		svc.SetBackupDir(filepath.Join(dir, "backups"))
		ctx, cancel := context.WithCancel(context.Background())
		if err := svc.StartRecorder(ctx, e.recordings, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
			t.Fatal(err)
		}
		e.srv = httptest.NewServer(Handler(svc, token, slog.New(slog.NewTextHandler(io.Discard, nil))))
		e.stop = func() {
			cancel()
			svc.WaitRecorder(5 * time.Second)
			e.srv.Close()
			svc.Close()
			st.Close()
			e.stop = func() {}
		}
	}
	e.start()
	t.Cleanup(func() { e.stop() })
	return e
}

func (e *env) do(t *testing.T, method, path string, body any, auth bool) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	if auth {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func wgConf(t *testing.T) string {
	key := make([]byte, 32)
	rand.Read(key)
	k := base64.StdEncoding.EncodeToString(key)
	return fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.2.0.2/32\nDNS = 10.2.0.1\n[Peer]\nPublicKey = %s\nAllowedIPs = 0.0.0.0/0\nEndpoint = 127.0.0.1:51820\n", k, k)
}

func TestFullFlow(t *testing.T) {
	var hits atomic.Int32
	provider := fakeProvider(&hits)
	defer provider.Close()
	e := setup(t, true)

	if code, _ := e.do(t, "GET", "/api/v1/health", nil, false); code != 200 {
		t.Fatalf("health: %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/v1/subscriptions", nil, false); code != 401 {
		t.Fatalf("missing token accepted: %d", code)
	}

	code, body := e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "Proton IT", "config": wgConf(t)}, true)
	if code != 201 {
		t.Fatalf("add vpn: %d %s", code, body)
	}
	if code, body := e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "rotta", "config": "[Interface]\n"}, true); code != 400 {
		t.Fatalf("invalid conf accepted: %d %s", code, body)
	}

	sub := map[string]any{"name": "Provider B", "kind": "xtream", "vpn_id": 1,
		"server": provider.URL, "username": "mario", "password": password}
	if code, body := e.do(t, "POST", "/api/v1/subscriptions", sub, true); code != 201 {
		t.Fatalf("add subscription: %d %s", code, body)
	}
	noVPN := map[string]any{"name": "Senza VPN", "kind": "xtream", "vpn_id": 99, "server": provider.URL, "username": "u", "password": "p"}
	if code, _ := e.do(t, "POST", "/api/v1/subscriptions", noVPN, true); code != 400 {
		t.Fatalf("subscription without VPN accepted: %d", code)
	}
	if hits.Load() != 0 {
		t.Fatal("provider contacted before sync")
	}

	code, body = e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true)
	if code != 200 {
		t.Fatalf("sync: %d %s", code, body)
	}
	var res struct {
		Total map[string]int `json:"total"`
		Added int            `json:"added"`
	}
	json.Unmarshal(body, &res)
	if res.Total["live"] != 1 || res.Total["movie"] != 2 || res.Total["series"] != 1 || res.Added != 4 {
		t.Fatalf("sync result: %s", body)
	}

	_, body = e.do(t, "GET", "/api/v1/catalog/movie?q=1408", nil, true)
	var page struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(body, &page)
	if len(page.Items) != 1 || page.Items[0]["display_title"] != "1408" || page.Items[0]["year"] != float64(2007) {
		t.Fatalf("search: %s", body)
	}
	_, body = e.do(t, "GET", "/api/v1/categories/movie", nil, true)
	if !strings.Contains(string(body), "Horror") {
		t.Fatalf("categories: %s", body)
	}

	_, body = e.do(t, "GET", "/api/v1/subscriptions", nil, true)
	if strings.Contains(string(body), password) || !strings.Contains(string(body), `"max_connections":1`) {
		t.Fatalf("subscriptions view: %s", body)
	}
	raw, _ := os.ReadFile(e.dbPath)
	wal, _ := os.ReadFile(e.dbPath + "-wal")
	if bytes.Contains(raw, []byte(password)) || bytes.Contains(wal, []byte(password)) {
		t.Fatal("password stored in clear in the database")
	}
}

func TestNoProviderTrafficWithoutVPN(t *testing.T) {
	var hits atomic.Int32
	provider := fakeProvider(&hits)
	defer provider.Close()
	e := setup(t, false) // the tunnel never comes up
	e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN", "config": wgConf(t)}, true)
	e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "B", "kind": "xtream", "vpn_id": 1,
		"server": provider.URL, "username": "mario", "password": password}, true)

	code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true)
	if code == 200 || !strings.Contains(string(body), vpn.ErrNotConnected.Error()) {
		t.Fatalf("sync without VPN: %d %s", code, body)
	}
	if hits.Load() != 0 {
		t.Fatalf("provider contacted %d times without VPN", hits.Load())
	}
	_, body = e.do(t, "GET", "/api/v1/subscriptions", nil, true)
	if !strings.Contains(string(body), `"sync_status":"error"`) || strings.Contains(string(body), password) {
		t.Fatalf("status after failed sync: %s", body)
	}
}

func TestUpdateSubscriptionAndVPN(t *testing.T) {
	var hits atomic.Int32
	provider := fakeProvider(&hits)
	defer provider.Close()
	e := setup(t, true)
	e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN", "config": wgConf(t)}, true)
	e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "B", "kind": "xtream", "vpn_id": 1,
		"server": provider.URL, "username": "mario", "password": "sbagliata"}, true)
	if code, _ := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true); code == 200 {
		t.Fatal("sync with wrong password succeeded")
	}
	// Fix only the password: server and username are kept.
	if code, body := e.do(t, "PATCH", "/api/v1/subscriptions/1", map[string]any{"password": password, "name": "Provider B"}, true); code != 204 {
		t.Fatalf("patch: %d %s", code, body)
	}
	if code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true); code != 200 {
		t.Fatalf("sync after fixing the password: %d %s", code, body)
	}
	// An empty password keeps the stored one.
	e.do(t, "PATCH", "/api/v1/subscriptions/1", map[string]any{"password": ""}, true)
	if code, _ := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true); code != 200 {
		t.Fatal("empty password replaced the stored one")
	}
	_, body := e.do(t, "GET", "/api/v1/subscriptions", nil, true)
	if !strings.Contains(string(body), `"name":"Provider B"`) || strings.Contains(string(body), password) {
		t.Fatalf("list: %s", body)
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/subscriptions/1", map[string]any{"vpn_id": 9}, true); code != 400 {
		t.Fatal("unknown VPN accepted")
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/vpn/1", map[string]any{"name": "Proton IT", "config": "[Interface]\n"}, true); code != 400 {
		t.Fatal("invalid VPN config accepted")
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/vpn/1", map[string]any{"name": "Proton IT", "config": wgConf(t)}, true); code != 204 {
		t.Fatal("valid VPN update refused")
	}
	if _, body := e.do(t, "GET", "/api/v1/vpn", nil, true); !strings.Contains(string(body), "Proton IT") {
		t.Fatalf("vpn rename: %s", body)
	}
}

func TestStatusAndSchedule(t *testing.T) {
	e := setup(t, true)
	code, body := e.do(t, "GET", "/api/v1/status", nil, true)
	var st struct {
		Problems []struct{ Area string }
	}
	json.Unmarshal(body, &st)
	for _, p := range st.Problems {
		if p.Area != "disk" { // the disk of the machine running the tests may be nearly full
			t.Fatalf("problem on a new server: %s", body)
		}
	}
	if code != 200 || !strings.Contains(string(body), `"catalog_hour":4`) {
		t.Fatalf("status: %d %s", code, body)
	}
	if code, _ := e.do(t, "PUT", "/api/v1/settings/schedule", map[string]int{"catalog_hour": 25, "guide_hours": 6}, true); code != 400 {
		t.Fatal("invalid hour accepted")
	}
	if code, _ := e.do(t, "PUT", "/api/v1/settings/schedule", map[string]int{"catalog_hour": 3, "guide_hours": 4}, true); code != 204 {
		t.Fatal("schedule refused")
	}
	if _, body := e.do(t, "GET", "/api/v1/settings/schedule", nil, true); string(bytes.TrimSpace(body)) != `{"catalog_hour":3,"guide_hours":4}` {
		t.Fatalf("schedule: %s", body)
	}
	if code, _ := e.do(t, "GET", "/api/v1/status", nil, false); code != 401 {
		t.Fatal("status without token")
	}
}

// A VPN the user runs outside VodMax (device or router): the provider is
// reached through the normal network, no tunnel is started.
func TestSystemVPN(t *testing.T) {
	var hits atomic.Int32
	provider := fakeProvider(&hits)
	defer provider.Close()
	e := setup(t, false) // an internal tunnel would never come up
	if code, _ := e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "Router", "kind": "altro"}, true); code != 400 {
		t.Fatal("unknown kind accepted")
	}
	if code, body := e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN del router", "kind": "system"}, true); code != 201 {
		t.Fatalf("add: %d %s", code, body)
	}
	e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "B", "kind": "xtream", "vpn_id": 1,
		"server": provider.URL, "username": "mario", "password": password}, true)
	if code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true); code != 200 {
		t.Fatalf("sync through the system network: %d %s", code, body)
	}
	_, body := e.do(t, "GET", "/api/v1/vpn", nil, true)
	if !strings.Contains(string(body), `"kind":"system"`) || !strings.Contains(string(body), `"connected":true`) {
		t.Fatalf("vpn: %s", body)
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/vpn/1", map[string]any{"config": wgConf(t)}, true); code != 400 {
		t.Fatal("configuration accepted on a system VPN")
	}
	if _, body := e.do(t, "GET", "/api/v1/status", nil, true); strings.Contains(string(body), `"area":"vpn"`) {
		t.Fatalf("system VPN reported as a problem: %s", body)
	}
}
