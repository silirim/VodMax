package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func richProvider() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "":
			w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","max_connections":"1"}}`))
		case "get_live_categories":
			w.Write([]byte(`[{"category_id":"1","category_name":"Italia"},{"category_id":"2","category_name":"Italia 4K - FHD"},
				{"category_id":"3","category_name":"Live UK"},{"category_id":"4","category_name":"Dazn Locale"}]`))
		case "get_vod_categories":
			w.Write([]byte(`[{"category_id":"10","category_name":"Horror"},{"category_id":"11","category_name":"XXX Misti"}]`))
		case "get_live_streams":
			w.Write([]byte(`[{"name":"----Italia----","stream_id":100,"category_id":"1"},
				{"name":"Rai 1 HD","stream_id":101,"category_id":"1"},{"name":"Rai 1 4K","stream_id":102,"category_id":"2"},
				{"name":"BBC One HD","stream_id":103,"category_id":"3"},{"name":"DAZN Calcio 1 HD","stream_id":104,"category_id":"4"}]`))
		case "get_vod_streams":
			w.Write([]byte(`[{"name":"1408 (2007)","stream_id":555,"category_id":"10"},{"name":"1408 (2007) FHD","stream_id":556,"category_id":"10"},
				{"name":"Psycho (1960)","stream_id":557,"category_id":"10"},{"name":"xXx","stream_id":558,"category_id":"11"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
}

func TestSelectionFlow(t *testing.T) {
	provider := richProvider()
	defer provider.Close()
	e := setup(t, true)
	e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN", "config": wgConf(t)}, true)
	e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "B", "kind": "xtream", "vpn_id": 1,
		"server": provider.URL, "username": "u", "password": "p"}, true)
	code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true)
	if code != 200 {
		t.Fatalf("sync %d %s", code, body)
	}
	var res struct {
		Skipped   int `json:"skipped"`
		Selection map[string]struct {
			Available, Visible, Cards int
			Sections                  map[string]int
		} `json:"selection"`
	}
	json.Unmarshal(body, &res)
	live, movie := res.Selection["live"], res.Selection["movie"]
	// Separator skipped; UK hidden by default; Rai 1 HD+4K grouped.
	if res.Skipped != 1 || live.Available != 4 || live.Visible != 3 || live.Cards != 2 ||
		live.Sections["tv"] != 1 || live.Sections["sport"] != 1 {
		t.Fatalf("live default selection: %s", body)
	}
	// Adult hidden; 1408 HD/FHD grouped.
	if movie.Available != 4 || movie.Visible != 3 || movie.Cards != 2 {
		t.Fatalf("movie default selection: %s", body)
	}

	var page struct {
		Items []struct {
			ID           int64  `json:"id"`
			DisplayTitle string `json:"display_title"`
			Quality      int    `json:"quality"`
			VariantCount int    `json:"variant_count"`
		} `json:"items"`
	}
	_, body = e.do(t, "GET", "/api/v1/catalog/live?section=tv", nil, true)
	json.Unmarshal(body, &page)
	if len(page.Items) != 1 || page.Items[0].DisplayTitle != "Rai 1" || page.Items[0].Quality != 4 || page.Items[0].VariantCount != 2 {
		t.Fatalf("TV section: %s", body)
	}
	code, body = e.do(t, "GET", "/api/v1/items/"+strconv.FormatInt(page.Items[0].ID, 10)+"/variants", nil, true)
	var variants []map[string]any
	json.Unmarshal(body, &variants)
	if code != 200 || len(variants) != 2 || variants[0]["quality"].(float64) < variants[1]["quality"].(float64) {
		t.Fatalf("variants: %d %s", code, body)
	}

	// Preview does not change anything; saving does.
	rules := map[string]any{"countries": []string{"UK"}, "movie_min_year": 1970, "hide_unknown_year": true}
	_, body = e.do(t, "POST", "/api/v1/subscriptions/1/rules/preview", rules, true)
	var stats map[string]struct{ Visible, Cards int }
	json.Unmarshal(body, &stats)
	if stats["live"].Visible != 4 || stats["movie"].Visible != 2 {
		t.Fatalf("preview: %s", body)
	}
	_, body = e.do(t, "GET", "/api/v1/catalog/live?section=tv", nil, true)
	if json.Unmarshal(body, &page); len(page.Items) != 1 {
		t.Fatalf("preview changed the catalog: %s", body)
	}
	if code, body := e.do(t, "PUT", "/api/v1/subscriptions/1/rules", rules, true); code != 200 {
		t.Fatalf("save rules: %d %s", code, body)
	}
	_, body = e.do(t, "GET", "/api/v1/catalog/movie", nil, true)
	if json.Unmarshal(body, &page); len(page.Items) != 1 || page.Items[0].DisplayTitle != "1408" {
		t.Fatalf("min year not applied: %s", body)
	}

	// Manual corrections: move Dazn to TV, exclude Horror.
	if code, body := e.do(t, "PUT", "/api/v1/subscriptions/1/categories/live/4", map[string]any{"section": "tv"}, true); code != 204 {
		t.Fatalf("category override: %d %s", code, body)
	}
	if code, _ := e.do(t, "PUT", "/api/v1/subscriptions/1/categories/live/4", map[string]any{"section": "cinema"}, true); code != 400 {
		t.Fatal("invalid section accepted")
	}
	e.do(t, "PUT", "/api/v1/subscriptions/1/categories/movie/10", map[string]any{"enabled": false}, true)
	_, body = e.do(t, "GET", "/api/v1/subscriptions/1/selection", nil, true)
	json.Unmarshal(body, &res.Selection)
	if res.Selection["live"].Sections["tv"] != 3 || res.Selection["movie"].Visible != 0 {
		t.Fatalf("after overrides: %s", body)
	}
	_, body = e.do(t, "GET", "/api/v1/subscriptions/1/categories?kind=movie", nil, true)
	var cats []struct {
		ID        string `json:"id"`
		Effective struct {
			Enabled bool   `json:"enabled"`
			Reason  string `json:"reason"`
		} `json:"effective"`
	}
	json.Unmarshal(body, &cats)
	for _, c := range cats {
		if c.Effective.Enabled || c.Effective.Reason == "" {
			t.Fatalf("category %s should be excluded with a reason: %s", c.ID, body)
		}
	}

	// Selection survives a new synchronisation.
	e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true)
	_, body = e.do(t, "GET", "/api/v1/subscriptions/1/selection", nil, true)
	json.Unmarshal(body, &res.Selection)
	if res.Selection["live"].Sections["tv"] != 3 || res.Selection["movie"].Visible != 0 {
		t.Fatalf("selection lost after re-sync: %s", body)
	}
}

// An international list (every category names its country): everything
// shows until the country is chosen at the first start; then only that
// country and the categories naming none (decisions 0042 and 0043).
func TestHomeCountryFirstStart(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "":
			w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","max_connections":"1"}}`))
		case "get_live_categories":
			w.Write([]byte(`[{"category_id":"1","category_name":"UK | Entertainment"},{"category_id":"2","category_name":"|DE| Sport"},{"category_id":"3","category_name":"Radio"}]`))
		case "get_live_streams":
			w.Write([]byte(`[{"name":"BBC One","stream_id":101,"category_id":"1"},{"name":"Sport DE","stream_id":102,"category_id":"2"},{"name":"Radio One","stream_id":103,"category_id":"3"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer provider.Close()
	e := setup(t, true)
	e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN", "config": wgConf(t)}, true)
	e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "B", "kind": "xtream", "vpn_id": 1,
		"server": provider.URL, "username": "u", "password": "p"}, true)
	if code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true); code != 200 {
		t.Fatalf("sync %d %s", code, body)
	}
	visible := func() int {
		_, body := e.do(t, "GET", "/api/v1/subscriptions/1/selection", nil, true)
		var s map[string]struct{ Visible int }
		json.Unmarshal(body, &s)
		return s["live"].Visible
	}
	var c struct {
		Home      string   `json:"home"`
		Countries []string `json:"countries"`
	}
	_, body := e.do(t, "GET", "/api/v1/settings/country", nil, true)
	if json.Unmarshal(body, &c); c.Home != "" || len(c.Countries) < 40 || visible() != 3 {
		t.Fatalf("before the choice: %s, %d visible", body, visible())
	}
	if code, _ := e.do(t, "PUT", "/api/v1/settings/country", map[string]string{"home": "EN"}, true); code != 400 {
		t.Fatal("a language group is not a country to live in")
	}
	if code, body := e.do(t, "PUT", "/api/v1/settings/country", map[string]string{"home": "gb"}, true); code != 204 {
		t.Fatalf("choice: %d %s", code, body)
	}
	if v := visible(); v != 2 { // BBC One and the radio
		t.Fatalf("after choosing GB: %d visible", v)
	}
	_, body = e.do(t, "GET", "/api/v1/subscriptions/1/rules", nil, true)
	if !strings.Contains(string(body), `"home":"GB"`) {
		t.Fatalf("rules show the chosen country: %s", body)
	}
	// A subscription's own choice wins.
	e.do(t, "PUT", "/api/v1/subscriptions/1/rules", map[string]any{"home": "DE"}, true)
	if v := visible(); v != 2 { // Sport DE and the radio
		t.Fatalf("own choice DE: %d visible", v)
	}
}
