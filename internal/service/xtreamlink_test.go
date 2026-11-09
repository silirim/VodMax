package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
)

func TestXtreamLink(t *testing.T) {
	cases := []struct {
		link, server, user, pass string
	}{
		{"http://panel.example:8080/get.php?username=mario&password=segreta&type=m3u_plus&output=ts", "http://panel.example:8080", "mario", "segreta"},
		{"https://panel.example/get.php?username=mario&password=p%40ss", "https://panel.example", "mario", "p@ss"},
		{" http://panel.example:2095/x/y/get.php?type=m3u&username=u&password=p ", "http://panel.example:2095/x/y", "u", "p"},
		{"http://ospite:chiave@panel.example/get.php?username=u&password=p", "http://panel.example", "u", "p"},
		{"http://panel.example:8080/player_api.php?username=mario&password=segreta", "http://panel.example:8080", "mario", "segreta"},
		{"https://panel.example/x/player_api.php?username=u&password=p&action=get_live_streams", "https://panel.example/x", "u", "p"},
		{"http://panel.example/player_api.php?username=mario", "", "", ""},
		{"http://panel.example/player_api.php/x?username=u&password=p", "", "", ""},
		// Plain playlists stay M3U.
		{"http://panel.example/get.php?username=mario&type=m3u_plus", "", "", ""},
		{"http://panel.example/get.php?username=mario&password=", "", "", ""},
		{"http://panel.example/lista.m3u?username=u&password=p", "", "", ""},
		{"http://panel.example/get.php/lista.m3u?username=u&password=p", "", "", ""},
		{"ftp://panel.example/get.php?username=u&password=p", "", "", ""},
		{"panel.example/get.php?username=u&password=p", "", "", ""},
	}
	for _, c := range cases {
		server, user, pass, ok := xtreamLink(c.link)
		if ok != (c.server != "") || server != c.server || user != c.user || pass != c.pass {
			t.Errorf("%q → %q %q %q %v", c.link, server, user, pass, ok)
		}
	}
}

// fakePanel answers player_api.php under prefix like an Xtream Codes panel,
// and refuses get.php with HTTP 884 as some panels do.
func fakePanel(t *testing.T, prefix string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == prefix+"/get.php":
			w.WriteHeader(884)
		case r.URL.Path != prefix+"/player_api.php":
			http.NotFound(w, r)
		case q.Get("username") != "mario" || q.Get("password") != "segreta":
			w.Write([]byte(`{"user_info":{"auth":0}}`))
		case q.Get("action") == "":
			w.Write([]byte(`{"user_info":{"auth":1,"status":"Active","exp_date":"1893456000","max_connections":"1","active_cons":"0"}}`))
		case q.Get("action") == "get_live_categories":
			w.Write([]byte(`[{"category_id":"1","category_name":"Notizie"}]`))
		case q.Get("action") == "get_live_streams":
			w.Write([]byte(`[{"num":1,"name":"Canale Uno","stream_id":101,"category_id":"1"}]`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func linkService(t *testing.T) (*Service, int64) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	vpnID, err := st.AddVPN(context.Background(), "v", "system", func(int64) []byte { return []byte("x") })
	if err != nil {
		t.Fatal(err)
	}
	return New(st, box, nil), vpnID
}

// checkXtream fails unless subscription id is an Xtream one with the
// link's server, user and password, and syncs it.
func checkXtream(t *testing.T, s *Service, id int64, server string) {
	t.Helper()
	ctx := context.Background()
	sub, err := s.st.Subscription(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var set xtreamSettings
	json.Unmarshal(sub.Settings, &set)
	raw, err := s.box.Open(sub.Secret, subOwner(id))
	if err != nil {
		t.Fatal(err)
	}
	var sec subscriptionSecret
	json.Unmarshal(raw, &sec)
	if sub.Kind != "xtream" || set.Server != server || set.Username != "mario" || sec.Password != "segreta" || sec.URL != "" {
		t.Fatalf("stored %s %+v %+v", sub.Kind, set, sec)
	}
	res, err := s.Sync(ctx, id, false)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Total["live"] != 1 {
		t.Fatalf("sync: %+v", res)
	}
}

func TestM3ULinkBecomesXtream(t *testing.T) {
	ctx := context.Background()
	s, vpnID := linkService(t)
	panel := fakePanel(t, "/p")
	link := panel.URL + "/p/get.php?username=mario&password=segreta&type=m3u_plus&output=ts"

	// Added as M3U: saved as Xtream, so player_api.php is used.
	id, err := s.AddSubscription(ctx, SubscriptionInput{Name: "A", Kind: "m3u", VPNID: vpnID, URL: link})
	if err != nil {
		t.Fatal(err)
	}
	checkXtream(t, s, id, panel.URL+"/p")

	// A link without a password stays a playlist.
	plain, err := s.AddSubscription(ctx, SubscriptionInput{Name: "P", Kind: "m3u", VPNID: vpnID, URL: panel.URL + "/p/get.php?username=mario"})
	if err != nil {
		t.Fatal(err)
	}
	if sub, _ := s.st.Subscription(ctx, plain); sub.Kind != "m3u" {
		t.Fatalf("plain link saved as %s", sub.Kind)
	}

	// Saved as M3U by an earlier version: the sync says what to do and
	// does not change the subscription by itself.
	sec, _ := json.Marshal(subscriptionSecret{URL: link})
	old, err := s.st.AddSubscription(ctx, store.Subscription{Name: "B", Kind: "m3u", VPNID: vpnID, Settings: json.RawMessage(`{"host":"127.0.0.1"}`)},
		func(id int64) []byte { return s.box.Seal(sec, subOwner(id)) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(ctx, old, false); err == nil || !strings.Contains(err.Error(), "risposta HTTP 884") || !strings.Contains(err.Error(), "Xtream Codes") {
		t.Fatalf("sync of get.php refused: %v", err)
	}
	sub, _ := s.st.Subscription(ctx, old)
	if sub.Kind != "m3u" || !strings.Contains(sub.SyncError, "modifica l'abbonamento") {
		t.Fatalf("after sync: %s %q", sub.Kind, sub.SyncError)
	}
	if pl := TranslateText("pl", sub.SyncError); pl == sub.SyncError || !strings.Contains(pl, "884") {
		t.Fatalf("pl: %q", pl)
	}
	// Saving it, even without typing the link again, converts it.
	name := "B"
	if err := s.UpdateSubscription(ctx, old, SubscriptionPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	checkXtream(t, s, old, panel.URL+"/p")

	// A get.php playlist that works keeps its kind when only renamed.
	working, err := s.st.AddSubscription(ctx, store.Subscription{Name: "C", Kind: "m3u", VPNID: vpnID, Settings: json.RawMessage(`{"host":"127.0.0.1"}`)},
		func(id int64) []byte { return s.box.Seal(sec, subOwner(id)) })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.st.SetSyncResult(ctx, working, "ok", "", nil); err != nil {
		t.Fatal(err)
	}
	name = "C2"
	if err := s.UpdateSubscription(ctx, working, SubscriptionPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if sub, _ := s.st.Subscription(ctx, working); sub.Kind != "m3u" {
		t.Fatalf("working playlist renamed: saved as %s", sub.Kind)
	}
}

func TestXtreamServerLink(t *testing.T) {
	ctx := context.Background()
	s, vpnID := linkService(t)
	panel := fakePanel(t, "/p")

	// The full link as the server: split, and it wins over the fields.
	link := panel.URL + "/p/player_api.php?username=mario&password=segreta"
	id, err := s.AddSubscription(ctx, SubscriptionInput{Name: "A", Kind: "xtream", VPNID: vpnID, Server: link, Username: "altro", Password: "vecchia"})
	if err != nil {
		t.Fatal(err)
	}
	checkXtream(t, s, id, panel.URL+"/p")

	// Edited with a get.php link, the password field left empty.
	old, err := s.AddSubscription(ctx, SubscriptionInput{Name: "B", Kind: "xtream", VPNID: vpnID, Server: panel.URL + "/vecchio", Username: "luigi", Password: "vecchia"})
	if err != nil {
		t.Fatal(err)
	}
	server, user, empty := panel.URL+"/p/get.php?username=mario&password=segreta&type=m3u_plus", "luigi", ""
	if err := s.UpdateSubscription(ctx, old, SubscriptionPatch{Server: &server, Username: &user, Password: &empty}); err != nil {
		t.Fatal(err)
	}
	checkXtream(t, s, old, panel.URL+"/p")

	// A link without the password is still refused.
	half := panel.URL + "/p/get.php?username=mario"
	if _, err := s.AddSubscription(ctx, SubscriptionInput{Name: "C", Kind: "xtream", VPNID: vpnID, Server: half, Username: "mario", Password: "segreta"}); err == nil {
		t.Fatal("add: link without password accepted")
	}
	if err := s.UpdateSubscription(ctx, old, SubscriptionPatch{Server: &half}); err == nil {
		t.Fatal("edit: link without password accepted")
	}

	// What the settings page sees has no password.
	subs, err := s.Subscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	shown, _ := json.Marshal(subs)
	if strings.Contains(string(shown), "segreta") || strings.Contains(string(shown), "password") {
		t.Fatalf("view: %s", shown)
	}

	// A full link saved as the server by an earlier version comes back
	// masked from the form: its secrets are kept, not replaced by ***.
	settings, _ := json.Marshal(xtreamSettings{Server: panel.URL + "/p/player_api.php?username=mario&password=segreta", Username: "mario"})
	sec, _ := json.Marshal(subscriptionSecret{Password: "segreta"})
	legacy, err := s.st.AddSubscription(ctx, store.Subscription{Name: "D", Kind: "xtream", VPNID: vpnID, Settings: settings},
		func(id int64) []byte { return s.box.Seal(sec, subOwner(id)) })
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.st.Subscription(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.subscriptionView(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	var masked xtreamSettings
	json.Unmarshal(view.Settings, &masked)
	if !strings.Contains(masked.Server, "password=***") {
		t.Fatalf("legacy view: %s", masked.Server)
	}
	name := "D2"
	if err := s.UpdateSubscription(ctx, legacy, SubscriptionPatch{Name: &name, Server: &masked.Server, Username: &masked.Username, Password: &empty}); err != nil {
		t.Fatal(err)
	}
	checkXtream(t, s, legacy, panel.URL+"/p")
}
