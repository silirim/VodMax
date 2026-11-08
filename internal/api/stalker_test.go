package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakePortal is a Stalker portal for one MAC; tokens expire on demand.
type fakePortal struct {
	*httptest.Server
	token  atomic.Int32 // current valid token number
	logins atomic.Int32
	links  atomic.Int32
	plays  atomic.Int32
	badMAC bool
}

func newFakePortal(t *testing.T) *fakePortal {
	p := &fakePortal{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/play/") {
			if r.URL.Query().Get("tok") != fmt.Sprint("link", p.links.Load()) {
				w.WriteHeader(403)
				return
			}
			p.plays.Add(1)
			w.Header().Set("Content-Type", "video/mp2t")
			w.Write([]byte(strings.Repeat("\x47"+strings.Repeat("\x00", 187), 50)))
			return
		}
		if r.URL.Path != "/stalker_portal/server/load.php" {
			w.WriteHeader(404)
			return
		}
		q := r.URL.Query()
		if !strings.Contains(r.Header.Get("Cookie"), "mac=00%3A1A%3A79%3A12%3A34%3A56") || p.badMAC {
			w.Write([]byte(`{"js":null}`))
			return
		}
		if q.Get("action") == "handshake" {
			n := p.logins.Add(1)
			p.token.Store(n)
			fmt.Fprintf(w, `{"js":{"token":"T%d"}}`, n)
			return
		}
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer T%d", p.token.Load()) {
			w.Write([]byte("Authorization failed."))
			return
		}
		switch q.Get("action") {
		case "get_profile":
			w.Write([]byte(`{"js":{"id":"7","name":"box"}}`))
		case "get_genres":
			w.Write([]byte(`{"js":[{"id":"*","title":"All"},{"id":"10","title":"Italia"},{"id":"11","title":"Sport"}]}`))
		case "get_all_channels":
			w.Write([]byte(`{"js":{"total_items":3,"data":[
				{"id":"501","name":"Rai 1 HD","number":"1","cmd":"ffmpeg http://localhost/ch/501","logo":"","tv_genre_id":"10","xmltv_id":"rai1.it"},
				{"id":"502","name":"Sport Uno","number":"2","cmd":"ffmpeg http://localhost/ch/502","tv_genre_id":11,"xmltv_id":""},
				{"id":"503","name":"Senza comando","number":"3","cmd":"","tv_genre_id":"10"},
				{"id":"504","name":24,"number":"4","cmd":"ffmpeg http://localhost/ch/504","logo":false,"tv_genre_id":"10","xmltv_id":null},
				"rotto"]}}`)) // loose types and a broken entry: the rest still syncs
		case "create_link":
			if q.Get("cmd") != "ffmpeg http://localhost/ch/501" && q.Get("cmd") != "ffmpeg http://localhost/ch/502" {
				w.Write([]byte(`{"js":{"cmd":""}}`))
				return
			}
			n := p.links.Add(1)
			fmt.Fprintf(w, `{"js":{"id":"1","cmd":"ffmpeg %s/play/%s?tok=link%d"}}`, p.URL, strings.TrimPrefix(q.Get("cmd"), "ffmpeg http://localhost/ch/"), n)
		default:
			w.Write([]byte(`{"js":null}`))
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func TestStalkerPortal(t *testing.T) {
	e := setup(t, true)
	p := newFakePortal(t)
	e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN", "config": wgConf(t)}, true)
	sub := map[string]any{"name": "Portale", "kind": "stalker", "vpn_id": 1, "server": p.URL + "/stalker_portal/c/", "mac": "00:1a:79:12:34"}
	if code, body := e.do(t, "POST", "/api/v1/subscriptions", sub, true); code != 400 || !strings.Contains(string(body), "MAC") {
		t.Fatalf("bad MAC accepted: %d %s", code, body)
	}
	sub["mac"] = "00-1a-79-12-34-56"
	if code, body := e.do(t, "POST", "/api/v1/subscriptions", sub, true); code != 201 {
		t.Fatalf("add: %d %s", code, body)
	}
	if _, body := e.do(t, "GET", "/api/v1/subscriptions", nil, true); strings.Contains(string(body), "12:34:56") {
		t.Fatalf("MAC returned: %s", body)
	}
	code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync?wait=true", nil, true)
	if code != 200 || !strings.Contains(string(body), `"live":3`) {
		t.Fatalf("sync: %d %s", code, body)
	}
	all := channels(t, e, "")
	if len(all) != 3 {
		t.Fatalf("channels: %+v", all)
	}
	var rai tvChannel
	for _, c := range all {
		if strings.HasPrefix(c.Name, "Rai 1") {
			rai = c
		}
	}
	if rai.ID == 0 {
		t.Fatalf("no Rai 1: %+v", all)
	}
	resp, err := openStream(t, e, ticket(t, e, rai.ID, "salotto-tv-01"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("play: %v %v", err, resp.Status)
	}
	readSome(t, resp)
	resp.Body.Close()
	logins := p.logins.Load()
	// The portal forgets the session: VodMax logs in again by itself.
	p.token.Store(99)
	resp, err = openStream(t, e, ticket(t, e, rai.ID, "salotto-tv-01"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("play after expiry: %v %v", err, resp.Status)
	}
	readSome(t, resp)
	resp.Body.Close()
	if p.logins.Load() != logins+1 || p.links.Load() != 2 || p.plays.Load() != 2 {
		t.Fatalf("logins %d→%d, links %d, plays %d", logins, p.logins.Load(), p.links.Load(), p.plays.Load())
	}
	// A MAC the portal does not know: the sync says so.
	p.badMAC = true
	if code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync?wait=true", nil, true); code == 200 || !strings.Contains(string(body), "MAC") {
		t.Fatalf("unknown MAC: %d %s", code, body)
	}
}

// An M3U playlist's channels play through the server too.
func TestM3ULiveThroughServer(t *testing.T) {
	e := setup(t, true)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/list.m3u":
			fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"news.it\" group-title=\"Italia\",News 24\n%s/stream/news.ts\n", srv.URL)
		case "/stream/news.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			w.Write([]byte(strings.Repeat("\x47"+strings.Repeat("\x00", 187), 50)))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN", "config": wgConf(t)}, true)
	if code, body := e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "Lista", "kind": "m3u", "vpn_id": 1, "url": srv.URL + "/list.m3u"}, true); code != 201 {
		t.Fatalf("add: %d %s", code, body)
	}
	if code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync?wait=true", nil, true); code != 200 {
		t.Fatalf("sync: %d %s", code, body)
	}
	list := channels(t, e, "")
	if len(list) != 1 {
		t.Fatalf("channels: %+v", list)
	}
	resp, err := openStream(t, e, ticket(t, e, list[0].ID, "salotto-tv-01"))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("play: %v %v", err, resp.Status)
	}
	readSome(t, resp)
	resp.Body.Close()
}
