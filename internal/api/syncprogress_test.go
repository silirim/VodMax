package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type syncView struct {
	Syncing   bool           `json:"syncing"`
	SyncPhase string         `json:"sync_phase"`
	SyncCount int            `json:"sync_count"`
	Counts    map[string]int `json:"counts"`
	LastSync  *struct {
		Total map[string]int `json:"total"`
		Error string         `json:"error"`
	} `json:"last_sync"`
}

func (e *env) waitSync(t *testing.T, ok func(syncView) bool) syncView {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); ; {
		_, body := e.do(t, "GET", "/api/v1/subscriptions", nil, true)
		var subs []syncView
		if err := json.Unmarshal(body, &subs); err != nil || len(subs) != 1 {
			t.Fatalf("subscriptions: %s", body)
		}
		if ok(subs[0]) {
			return subs[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscription: %s", body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The sync runs in the background: the answer comes while the provider is
// still sending, the list shows the phase, the catalog is there while the
// guide loads, and the result stays for the page after the end.
func TestSyncInBackground(t *testing.T) {
	films, guide := make(chan struct{}), make(chan struct{})
	var once [2]sync.Once
	freeFilms := func() { once[0].Do(func() { close(films) }) }
	freeGuide := func() { once[1].Do(func() { close(guide) }) }
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/xmltv.php" {
			<-guide
			w.Write([]byte(`<?xml version="1.0"?><tv><channel id="rai1.it"><display-name>Rai 1</display-name></channel></tv>`))
			return
		}
		switch r.URL.Query().Get("action") {
		case "":
			w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","exp_date":"1893456000","max_connections":"1","active_cons":"0"}}`))
		case "get_live_categories":
			w.Write([]byte(`[{"category_id":"1","category_name":"Italia"}]`))
		case "get_vod_categories":
			w.Write([]byte(`[{"category_id":"10","category_name":"Horror"}]`))
		case "get_live_streams":
			w.Write([]byte(`[{"num":1,"name":"Rai 1 HD","stream_id":101,"category_id":"1","epg_channel_id":"rai1.it"}]`))
		case "get_vod_streams":
			// Half of the list, then the provider stalls until films is closed.
			w.Write([]byte("["))
			for i := range 1500 {
				if i > 0 {
					w.Write([]byte(","))
				}
				fmt.Fprintf(w, `{"name":"Film numero %d","stream_id":%d,"category_id":"10"}`, i, 1000+i)
			}
			w.(http.Flusher).Flush()
			<-films
			w.Write([]byte("]"))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	defer provider.Close()
	defer freeGuide()
	defer freeFilms()
	e := setup(t, true)
	e.do(t, "POST", "/api/v1/vpn", map[string]string{"name": "VPN", "config": wgConf(t)}, true)
	e.do(t, "POST", "/api/v1/subscriptions", map[string]any{"name": "B", "kind": "xtream", "vpn_id": 1,
		"server": provider.URL, "username": "mario", "password": password}, true)

	start := time.Now()
	code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true)
	var started syncView
	json.Unmarshal(body, &started)
	if code != http.StatusAccepted || !started.Syncing || time.Since(start) > 5*time.Second {
		t.Fatalf("start: %d %s after %v", code, body, time.Since(start))
	}
	e.waitSync(t, func(v syncView) bool { return v.SyncPhase == "Scarico i film" && v.SyncCount == 1000 })
	// Pressed again: the running sync, not an error.
	if code, body := e.do(t, "POST", "/api/v1/subscriptions/1/sync", nil, true); code != http.StatusAccepted || json.Unmarshal(body, &started) != nil || !started.Syncing {
		t.Fatalf("second start: %d %s", code, body)
	}

	freeFilms()
	v := e.waitSync(t, func(v syncView) bool { return v.SyncPhase == "Guida TV" })
	if v.Counts["movie"] != 1500 || !v.Syncing {
		t.Fatalf("while the guide loads: %+v", v)
	}
	if _, body := e.do(t, "GET", "/api/v1/catalog/movie?limit=5", nil, true); !containsItems(body) {
		t.Fatalf("catalog while the guide loads: %s", body)
	}

	freeGuide()
	v = e.waitSync(t, func(v syncView) bool { return !v.Syncing })
	if v.LastSync == nil || v.LastSync.Error != "" || v.LastSync.Total["movie"] != 1500 || v.LastSync.Total["live"] != 1 {
		t.Fatalf("after the end: %+v", v.LastSync)
	}
}

func containsItems(body []byte) bool {
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	return json.Unmarshal(body, &page) == nil && len(page.Items) > 0
}
