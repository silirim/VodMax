package service

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/silirim/vodmax/internal/catalog"
	"github.com/silirim/vodmax/internal/store"
)

// Saving a synced catalog gives the same results however it is written:
// what is stored is what the selection computes, the statistics of the
// sync are those of the subscription, ids stay across syncs, and the
// search finds the titles by their current names.
func TestSyncSaveKeepsResults(t *testing.T) {
	ctx := context.Background()
	s, vpnID := linkService(t)
	if err := s.SetHomeCountry(ctx, "IT"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	films := map[int]string{}
	for i := range 300 {
		films[i] = fmt.Sprintf("Film numero %d (%d)", i, 1990+i%30)
	}
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var out any
		switch r.URL.Query().Get("action") {
		case "":
			out = map[string]any{"user_info": map[string]any{"auth": 1, "status": "Active", "exp_date": "1893456000", "max_connections": "1", "active_cons": "0"}}
		case "get_live_categories":
			out = []map[string]string{{"category_id": "1", "category_name": "IT | Intrattenimento"}, {"category_id": "2", "category_name": "DE | Unterhaltung"}}
		case "get_vod_categories":
			out = []map[string]string{{"category_id": "10", "category_name": "IT | Film"}, {"category_id": "11", "category_name": "DE | Filme"},
				{"category_id": "12", "category_name": "XXX | Adulti"}}
		case "get_live_streams":
			var list []map[string]any
			for i := range 40 {
				for v, q := range []string{" HD", " SD"}[:1+i%2] {
					list = append(list, map[string]any{"num": i + 1, "name": fmt.Sprintf("Canale %d%s", i, q), "stream_id": 100 + 2*i + v,
						"category_id": fmt.Sprint(1 + i%2)})
				}
			}
			out = list
		case "get_vod_streams":
			mu.Lock()
			var list []map[string]any
			for i, name := range films {
				list = append(list, map[string]any{"name": name, "stream_id": 1000 + i, "category_id": fmt.Sprint(10 + i%3), "container_extension": "mkv"})
				if i%10 == 0 { // a second version of the same film
					list = append(list, map[string]any{"name": name + " 4K", "stream_id": 5000 + i, "category_id": fmt.Sprint(10 + i%3), "container_extension": "mkv"})
				}
			}
			mu.Unlock()
			slices.SortFunc(list, func(a, b map[string]any) int { return a["stream_id"].(int) - b["stream_id"].(int) })
			out = list
		default:
			out = []any{}
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(panel.Close)
	id, err := s.AddSubscription(ctx, SubscriptionInput{Name: "A", Kind: "xtream", VPNID: vpnID, Server: panel.URL, Username: "mario", Password: "segreta"})
	if err != nil {
		t.Fatal(err)
	}

	type state struct {
		ids    map[string]int64
		counts map[string]int
		stats  catalog.Stats
		found  []int64
	}
	run := func() state {
		t.Helper()
		res, err := s.Sync(ctx, id, false)
		if err != nil {
			t.Fatal(err)
		}
		// What is stored is what the selection computes now.
		rules, cats, items, err := s.loadSelection(ctx, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		stored := slices.Clone(items)
		catalog.Select(rules, cats, items)
		if !slices.Equal(items, stored) {
			t.Fatal("stored selection differs from the computed one")
		}
		if stats, err := s.SubscriptionStats(ctx, id, nil); err != nil || !reflect.DeepEqual(stats, res.Selection) {
			t.Fatalf("sync statistics %+v, subscription %+v (%v)", res.Selection, stats, err)
		}
		var st state
		st.ids, _ = s.st.ItemIDs(ctx, id, "movie")
		st.counts, _ = s.st.Counts(ctx, id)
		st.stats = res.Selection
		page, _ := s.st.Catalog(ctx, store.Query{Kind: "movie", Text: "numero 1", Limit: 200})
		for _, it := range page.Items {
			st.found = append(st.found, it.ID)
		}
		return st
	}

	first := run()
	// Italian films and channels are shown, German and adult ones not; a
	// film with two versions is one card.
	if first.counts["movie"] != 100 || first.counts["live"] != 20 || first.stats["movie"].Visible != 110 {
		t.Fatalf("first sync: %v %+v", first.counts, first.stats["movie"])
	}
	if len(first.found) == 0 {
		t.Fatal("search found nothing")
	}
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("the same catalog gave other results:\n%+v\n%+v", first, second)
	}

	mu.Lock()
	films[3] = "Pellicola rara (1993)"
	delete(films, 6)
	films[400] = "Film nuovo (2024)"
	mu.Unlock()
	third := run()
	if third.counts["movie"] != 99 || third.ids["1003"] != first.ids["1003"] || third.ids["1400"] == 0 || third.ids["1006"] != first.ids["1006"] {
		t.Fatalf("changed catalog: counts %v, ids %d→%d, new %d", third.counts, first.ids["1003"], third.ids["1003"], third.ids["1400"])
	}
	maps.DeleteFunc(third.ids, func(k string, _ int64) bool { return k == "1400" || k == "5400" }) // the new film, two versions
	if !maps.Equal(first.ids, third.ids) {
		t.Fatal("ids changed")
	}
	if page, _ := s.st.Catalog(ctx, store.Query{Kind: "movie", Text: "rara", Limit: 5}); len(page.Items) != 1 || page.Items[0].ID != first.ids["1003"] {
		t.Fatalf("renamed film: %+v", page.Items)
	}
}

// The list of subscriptions shows how far the save is, in the viewer's
// language, and a phase without a total shows none.
func TestSyncProgressInView(t *testing.T) {
	ctx := WithLang(context.Background(), "pl")
	s, sub := oneConnectionService(t)
	s.beginSync(sub)
	s.syncProgress(sub, "Salvo il catalogo", 45000, 220000)
	subs, err := s.Subscriptions(ctx)
	if err != nil || subs[0].SyncPhase != "Zapisywanie katalogu" || subs[0].SyncCount != 45000 || subs[0].SyncTotal != 220000 {
		t.Fatalf("%+v %v", subs, err)
	}
	s.syncPhase(sub, "Aggiorno l'indice di ricerca", 0)
	if subs, _ = s.Subscriptions(ctx); subs[0].SyncPhase != "Aktualizowanie indeksu wyszukiwania" || subs[0].SyncTotal != 0 {
		t.Fatalf("%+v", subs[0])
	}
}
