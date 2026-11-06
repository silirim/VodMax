package catalog

import (
	"testing"

	"github.com/silirim/vodmax/internal/store"
)

func cat(kind, id, name string) store.SelectionCategory {
	info := ClassifyCategory(kind, name)
	return store.SelectionCategory{SubscriptionID: 1, Category: store.Category{
		Kind: kind, SourceID: id, Name: name, Country: info.Country, Adult: info.Adult, LowRes: info.LowRes, Section: string(info.Section),
	}}
}

func item(id int64, kind, catID, title string, year int) store.SelectionItem {
	return store.SelectionItem{ID: id, SubscriptionID: 1, Kind: kind, CategoryID: catID,
		GroupKey: GroupKey(kind, title, year), Year: year, Quality: Quality(title, CategoryInfo{})}
}

func TestSelectDefaults(t *testing.T) {
	cats := []store.SelectionCategory{
		cat("live", "1", "Italia"), cat("live", "2", "Italia 4K - FHD"), cat("live", "3", "Live UK"),
		cat("live", "4", "XXX Live"), cat("live", "5", "SD (Connessioni Lente)"), cat("live", "6", "Dazn Locale"),
		cat("movie", "10", "Commedia"), cat("movie", "11", "XXX Misti"),
	}
	items := []store.SelectionItem{
		item(1, "live", "1", "Rai 1 HD", 0), item(2, "live", "2", "Rai 1 4K", 0), item(3, "live", "2", "Rai 1 FHD", 0),
		item(4, "live", "3", "BBC One HD", 0), item(5, "live", "4", "Hot", 0), item(6, "live", "5", "Rai 1 SD", 0),
		item(7, "live", "6", "DAZN Calcio 1 HD", 0),
		item(10, "movie", "10", "Amici miei", 1975), item(11, "movie", "10", "Amici miei", 1975),
		item(12, "movie", "11", "xXx", 0),
	}
	stats := Select(map[int64]Rules{}, cats, items)

	byID := map[int64]store.SelectionItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if !byID[2].Primary || byID[2].VariantCount != 3 || byID[1].Primary || !byID[1].Visible {
		t.Fatalf("Rai 1: the 4K variant must be primary of 3: %+v %+v", byID[1], byID[2])
	}
	for _, id := range []int64{4, 5, 6, 12} {
		if byID[id].Visible {
			t.Errorf("item %d should be hidden by default", id)
		}
	}
	if byID[7].Section != "sport" || byID[1].Section != "tv" {
		t.Errorf("sections: %q %q", byID[7].Section, byID[1].Section)
	}
	if s := stats["live"]; s.Available != 7 || s.Visible != 4 || s.Cards != 2 || s.Sections["tv"] != 1 || s.Sections["sport"] != 1 {
		t.Errorf("live stats %+v", *s)
	}
	if s := stats["movie"]; s.Visible != 2 || s.Cards != 1 {
		t.Errorf("movie stats %+v", *s)
	}
}

func TestSelectRulesAndOverrides(t *testing.T) {
	uk := cat("live", "3", "Live UK")
	off := false
	commedia := cat("movie", "10", "Commedia")
	commedia.Enabled = &off
	sport := cat("live", "7", "Canali vari")
	section := "sport"
	sport.SectionOverride = &section
	cats := []store.SelectionCategory{uk, commedia, sport, cat("movie", "12", "Dramma")}
	items := []store.SelectionItem{
		item(1, "live", "3", "BBC One HD", 0),
		item(2, "movie", "10", "Amici miei", 1975),
		item(3, "movie", "12", "Il sorpasso", 1962),
		item(4, "movie", "12", "Senza anno", 0),
		item(5, "movie", "12", "Recente", 2020),
		item(6, "live", "7", "Sportitalia", 0),
	}
	items[4].Hidden = true
	Select(map[int64]Rules{1: {Countries: []string{"UK"}, MovieMinYear: 1970, HideUnknownYear: true}}, cats, items)
	want := map[int64]bool{1: true, 2: false, 3: false, 4: false, 5: false, 6: true}
	for _, it := range items {
		if it.Visible != want[it.ID] {
			t.Errorf("item %d visible=%v, atteso %v", it.ID, it.Visible, want[it.ID])
		}
	}
	if items[5].Section != "sport" {
		t.Errorf("section override ignored: %q", items[5].Section)
	}
	e := EffectiveCategory(commedia, Rules{})
	if e.Enabled || e.Reason != "esclusa a mano" {
		t.Errorf("manual exclusion: %+v", e)
	}
}

// A title in a category the provider does not list is shown, but not to
// kids; one without category is plain home content.
func TestSelectUnlistedCategory(t *testing.T) {
	items := []store.SelectionItem{item(1, "movie", "99", "Nascosto", 2000), item(2, "live", "", "Senza gruppo", 0)}
	Select(map[int64]Rules{}, []store.SelectionCategory{cat("movie", "10", "Commedia")}, items)
	if !items[0].Visible || !items[0].Adult {
		t.Errorf("unlisted category: %+v", items[0])
	}
	if !items[1].Visible || items[1].Adult || items[1].Section != "tv" {
		t.Errorf("no category: %+v", items[1])
	}
}

func TestRulesValidation(t *testing.T) {
	if _, err := ParseRules([]byte(`{"movie_min_year": 1850}`)); err == nil {
		t.Error("year 1850 accepted")
	}
	if r, err := ParseRules([]byte(`{}`)); err != nil || r.Adult || len(r.Countries) != 0 {
		t.Errorf("defaults: %+v %v", r, err)
	}
}
