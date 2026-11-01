package xtream

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/provider"
)

// testDialer stands in for the VPN tunnel so the client can reach an
// httptest server. Production code only ever passes *vpn.Tunnel.
type testDialer struct{}

func (testDialer) Up() bool { return true }
func (testDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func fakeServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/player_api.php" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("username") != "mario" || q.Get("password") != "p@ss word" {
			w.Write([]byte(`{"user_info":{"auth":0}}`))
			return
		}
		switch q.Get("action") {
		case "":
			w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","exp_date":"1893456000","max_connections":"2","active_cons":0}}`))
		case "get_live_streams":
			// The middle entry is malformed (a string): left out, not fatal.
			w.Write([]byte(`[{"num":1,"name":"Rai 1","stream_id":101,"epg_channel_id":"rai1.it","category_id":"5","tv_archive":1,"tv_archive_duration":"7"},
				"rotto",
				{"num":"2","name":"Canale 5","stream_id":"102","epg_channel_id":null,"category_id":5,"tv_archive":0}]`))
		case "get_vod_streams":
			w.Write([]byte(`[{"name":"1408","stream_id":555,"rating":"7.1","container_extension":"mkv","tmdb":3021,"year":"2007"}]`))
		case "get_series_info":
			if q.Get("series_id") != "77" {
				w.Write([]byte(`{}`))
				return
			}
			w.Write([]byte(`{"info":{"name":"Twin Peaks","series_id":77},"episodes":{
				"1":[{"id":"9001","episode_num":1,"title":"Pilot","container_extension":"mp4","info":{"duration_secs":5400}}],
				"2":[{"id":9002,"episode_num":"1","title":"May the Giant Be with You","season":2,"info":{}}]}}`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
}

func newClient(t *testing.T, srv *httptest.Server, password string) *Client {
	c, err := New(srv.URL+"/player_api.php", "mario", password, provider.NewHTTPClient(testDialer{}, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoginAndCatalog(t *testing.T) {
	srv := fakeServer(t)
	defer srv.Close()
	c := newClient(t, srv, "p@ss word")
	ctx := context.Background()

	acct, err := c.Login(ctx)
	if err != nil || !acct.Active() || acct.MaxConnections.Int() != 2 || acct.Expires().Year() != 2030 {
		t.Fatalf("login: %+v %v", acct, err)
	}
	live, err := c.LiveStreams(ctx)
	if err != nil || len(live) != 2 || live[1].StreamID.String() != "102" || live[0].TVArchiveDuration.Int() != 7 || live[1].EPGChannelID != "" {
		t.Fatalf("live: %+v %v", live, err)
	}
	vod, err := c.VODStreams(ctx)
	if err != nil || vod[0].TMDB.String() != "3021" || vod[0].Rating.String() != "7.1" {
		t.Fatalf("vod: %+v %v", vod, err)
	}
	info, err := c.SeriesInfo(ctx, "77")
	if err != nil || info.Info.Name != "Twin Peaks" || len(info.Episodes) != 2 {
		t.Fatalf("series: %+v %v", info, err)
	}
	for _, ep := range info.Episodes {
		if ep.Season.Int() == 0 {
			t.Fatalf("season missing: %+v", ep)
		}
	}
}

func TestBadCredentials(t *testing.T) {
	srv := fakeServer(t)
	defer srv.Close()
	if _, err := newClient(t, srv, "wrong").Login(context.Background()); err == nil {
		t.Fatal("expected login failure")
	}
}

func TestURLsAndErrorsHideCredentials(t *testing.T) {
	srv := fakeServer(t)
	defer srv.Close()
	c := newClient(t, srv, "p@ss word")
	u := c.StreamURL(KindMovie, "555", "mkv")
	if !strings.HasSuffix(u, "/movie/mario/p@ss%20word/555.mkv") {
		t.Fatalf("stream url: %s", u)
	}
	start := time.Date(2026, 10, 28, 19, 45, 0, 0, time.UTC)
	if cu := c.CatchupURL("101", start, 2*time.Hour, time.UTC); !strings.HasSuffix(cu, "/timeshift/mario/p@ss%20word/120/2026-10-28:19-45/101.ts") {
		t.Fatalf("catchup url: %s", cu)
	}

	srv.Close() // force a transport error that would carry the URL
	_, err := c.LiveStreams(context.Background())
	if err == nil || strings.Contains(err.Error(), "p@ss") || strings.Contains(err.Error(), "mario") {
		t.Fatalf("credentials in error: %v", err)
	}
}

func TestRejectsBadServer(t *testing.T) {
	for _, s := range []string{"", "ftp://x", "provider.example"} {
		if _, err := New(s, "u", "p", nil); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}
