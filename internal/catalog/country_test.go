package catalog

import (
	"testing"

	"github.com/silirim/vodmax/internal/store"
)

func TestDetectCountry(t *testing.T) {
	cases := []struct{ kind, name, want string }{
		// Italian-market lists: foreign groups as "Live <Country>".
		{"live", "Live UK", "GB"},
		{"live", "Live Deutschland/Asterreich new", "DE"},
		{"live", "Live Deutschland Select", "DE"},
		{"live", "Live Shqiptar", "AL"},
		{"live", "Live Ellada", "GR"},
		{"live", "Live Alearab", "ARABIC"},
		{"live", "Live Slavic", "Slavic"}, // unknown names stay as written
		{"live", "Live Francia", "FR"},
		// Home groups that start with "Live"/"TV".
		{"live", "Live Calcio", ""},
		{"live", "Live Serie A", ""},
		{"live", "Live Sport", ""},
		{"live", "LIVE EVENTS", ""},
		{"live", "LIVE | PPV", ""},
		{"live", "TV Locali", ""},
		{"live", "TV Regionali", ""},
		{"live", "Live Radio", ""},
		// A sport or events group of a foreign country stays foreign.
		{"live", "Live Sport UK", "GB"},
		{"live", "LIVE EVENTS UK", "GB"},
		{"live", "Live Events DE", "DE"},
		{"live", "Live Calcio Spagna", "ES"},
		{"live", "LIVE SPORTS USA", "US"},
		{"live", "TV Sport France", "FR"},
		{"live", "Live Radio France", "FR"},
		{"live", "Live Sport IT", "IT"},
		{"live", "Live Sport Arabia", "ARABIC"},
		{"live", "Live Arabia", "ARABIC"},
		// Italian names of countries.
		{"live", "Live Croazia", "HR"},
		{"live", "Live Ucraina", "UA"},
		{"live", "Live Messico", "MX"},
		{"movie", "Cile | Cine", "CL"},
		{"series", "Filippine: Serie", "PH"},
		{"live", "Italia", "IT"},
		{"live", "Italia 4K - FHD", "IT"},
		{"live", "International Sport Channels", "INT"},
		// International lists: a code or a name, then a separator.
		{"live", "UK | Sports", "GB"},
		{"live", "|DE| Sport", "DE"},
		{"live", "FR: Cinéma", "FR"},
		{"live", "[ES] Deportes", "ES"},
		{"live", "IT - Rai", "IT"},
		{"live", "US| News", "US"},
		{"live", "EX-YU ► Sport", "EXYU"},
		{"live", "NO | Sport", "NO"},
		{"movie", "EN - Action", "EN"},
		{"movie", "Action (DE)", "DE"},
		{"movie", "United Kingdom | Comedy", "GB"},
		{"series", "TR: Diziler", "TR"},
		{"movie", "Germany Movies", "DE"},
		{"movie", "España Cine", "ES"},
		// Names that are not countries.
		{"live", "Calcio STAR", ""},
		{"live", "Intrattenimento 4K - FHD", ""},
		{"live", "Dazn 4K - SuperHD", ""},
		{"live", "F1 - MotoGP", ""},
		{"live", "SD (Connessioni Lente)", ""},
		{"live", "Campionato Serie A", ""},
		{"live", "Sport: Calcio", ""},
		{"movie", "Commedia Italiana", ""},
		{"movie", "Tom Hanks", ""},
		{"movie", "Live Aid concerti", ""}, // "Live" means a country only for channels
		{"series", "Serie Turche", ""},
		{"series", "Serie Netflix", ""},
		{"movie", "Film 4K (HDR)", ""},
	}
	for _, c := range cases {
		if got := DetectCountry(c.kind, c.name); got != c.want {
			t.Errorf("%s %q: %q, atteso %q", c.kind, c.name, got, c.want)
		}
	}
}

func TestCanonicalCountry(t *testing.T) {
	for in, want := range map[string]string{"UK": "GB", "gb": "GB", "England": "GB", "Deutschland/Asterreich": "DE",
		"United States": "US", "Internazionale": "INT", "IT": "IT", "Slavic": "Slavic", "": ""} {
		if got := CanonicalCountry(in); got != want {
			t.Errorf("%q: %q, atteso %q", in, got, want)
		}
	}
}

// Rules saved before "home" existed select exactly as before: plain names
// are home, "Live <Country>" groups are foreign, labels such as "UK" keep
// their group, and "Italia" (now recognised as IT) is not excluded.
func TestSelectOldRulesUnchanged(t *testing.T) {
	cats := []store.SelectionCategory{cat("live", "1", "Italia"), cat("live", "2", "Live UK"), cat("live", "3", "Live France"),
		cat("live", "4", "Calcio STAR"), cat("movie", "10", "Azione")}
	items := []store.SelectionItem{item(1, "live", "1", "Rai 1", 0), item(2, "live", "2", "BBC One", 0), item(3, "live", "3", "TF1", 0),
		item(4, "live", "4", "Sky Calcio 1", 0), item(10, "movie", "10", "Heat", 1995)}
	Select(map[int64]Rules{1: {Countries: []string{"UK"}}}, cats, items)
	want := map[int64]bool{1: true, 2: true, 3: false, 4: true, 10: true}
	for _, it := range items {
		if it.Visible != want[it.ID] {
			t.Errorf("item %d visibile=%v, atteso %v", it.ID, it.Visible, want[it.ID])
		}
	}
}

// An international list: every category names its country. Home decides.
func TestSelectHomeCountry(t *testing.T) {
	cats := []store.SelectionCategory{cat("live", "1", "UK | Entertainment"), cat("live", "2", "|DE| Sport"), cat("live", "3", "IT - Rai"),
		cat("movie", "10", "EN - Action"), cat("movie", "11", "DE: Filme"), cat("movie", "12", "Documentaries"), cat("series", "20", "UK | Drama")}
	items := []store.SelectionItem{item(1, "live", "1", "BBC One", 0), item(2, "live", "2", "Sky Sport DE", 0), item(3, "live", "3", "Rai 1", 0),
		item(10, "movie", "10", "Heat", 1995), item(11, "movie", "11", "Das Boot", 1981), item(12, "movie", "12", "Planet", 2006),
		item(20, "series", "20", "Sherlock", 2010)}
	visible := func(r Rules) map[int64]bool {
		its := append([]store.SelectionItem(nil), items...)
		Select(map[int64]Rules{1: r}, cats, its)
		out := map[int64]bool{}
		for _, it := range its {
			out[it.ID] = it.Visible
		}
		return out
	}
	// Before choosing, an international list shows everything (nothing
	// is in the "Live <Country>" form).
	for id, v := range visible(Rules{}) {
		if !v {
			t.Errorf("senza paese di casa l'elemento %d dovrebbe restare visibile", id)
		}
	}
	gb := visible(Rules{Home: "GB", Countries: []string{"EN"}})
	for id, want := range map[int64]bool{1: true, 2: false, 3: false, 10: true, 11: false, 12: true, 20: true} {
		if gb[id] != want {
			t.Errorf("da inglese: elemento %d visibile=%v, atteso %v", id, gb[id], want)
		}
	}
	de := visible(Rules{Home: "DE"})
	for id, want := range map[int64]bool{1: false, 2: true, 3: false, 10: false, 11: true, 12: true, 20: false} {
		if de[id] != want {
			t.Errorf("da tedesco: elemento %d visibile=%v, atteso %v", id, de[id], want)
		}
	}
	it := visible(Rules{Home: "IT", Countries: []string{"UK"}}) // an old label still names GB
	for id, want := range map[int64]bool{1: true, 2: false, 3: true, 10: false, 11: false, 12: true, 20: true} {
		if it[id] != want {
			t.Errorf("da italiano: elemento %d visibile=%v, atteso %v", id, it[id], want)
		}
	}
}

// A manual correction of the country wins over the name.
func TestCountryOverride(t *testing.T) {
	c := cat("live", "1", "UK | Sky Sports")
	de := "DE"
	c.CountryOverride = &de
	if e := EffectiveCategory(c, Rules{Home: "DE"}); !e.Enabled || e.Detected != "DE" {
		t.Fatalf("correzione: %+v", e)
	}
}
