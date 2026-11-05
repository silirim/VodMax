package catalog

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/silirim/vodmax/internal/store"
)

// Rules are the user's choices for one subscription: what to keep from the
// provider's list. The zero value keeps only home-country, non-adult,
// non-low-res content of any year.
type Rules struct {
	// Home is the person's country (decision 0042), a code such as "IT"
	// or "GB": its categories and those naming no country are kept. Empty
	// for rules saved before it existed: then only the "Live <Country>"
	// and "International" groups count as foreign, as they always did,
	// and categories naming no country are the home ones.
	Home string `json:"home"`
	// Countries lists other countries to keep, as codes ("GB"); labels
	// saved by older versions ("UK") are understood too.
	Countries []string `json:"countries"`
	Adult     bool     `json:"adult"`   // keep adult categories
	LowRes    bool     `json:"low_res"` // keep low-quality duplicate categories
	// Minimum release year for films and series; 0 = no limit.
	MovieMinYear  int `json:"movie_min_year"`
	SeriesMinYear int `json:"series_min_year"`
	// With a minimum year set, titles whose year is unknown are hidden too.
	HideUnknownYear bool `json:"hide_unknown_year"`
	// Prefer puts this subscription's variants first (local collections);
	// set by the server, not stored.
	Prefer bool `json:"-"`
}

// ParseRules accepts the stored JSON ("{}" means defaults).
func ParseRules(raw json.RawMessage) (Rules, error) {
	var r Rules
	if len(raw) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	return r, r.Validate()
}

func (r Rules) Validate() error {
	if len(r.Home) > 40 || len(r.Countries) > 300 {
		return fmt.Errorf("paesi non validi")
	}
	for _, y := range []int{r.MovieMinYear, r.SeriesMinYear} {
		if y != 0 && (y < 1900 || y > 2100) {
			return fmt.Errorf("anno minimo %d non valido", y)
		}
	}
	return nil
}

// Effective is a category after applying user overrides and rules.
type Effective struct {
	Country string `json:"country"`
	// Detected is the country the name points to in any spelling (or the
	// manual correction): what the selection page counts and offers.
	Detected string `json:"detected,omitempty"`
	Adult    bool   `json:"adult"`
	Section  string `json:"section,omitempty"`
	Enabled  bool   `json:"enabled"`
	// Reason explains why a category is excluded, for the user interface.
	Reason string `json:"reason,omitempty"`
}

// EffectiveCategory resolves a category against overrides and rules.
func EffectiveCategory(c store.SelectionCategory, r Rules) Effective {
	e := Effective{Country: c.Country, Adult: c.Adult, Section: c.Section, Enabled: true}
	e.Detected = DetectCountry(c.Kind, c.Name)
	if r.Home != "" {
		e.Country = e.Detected
	}
	if c.CountryOverride != nil {
		e.Country = CanonicalCountry(*c.CountryOverride)
		e.Detected = e.Country
	}
	e.Country = CanonicalCountry(e.Country)
	if c.AdultOverride != nil {
		e.Adult = *c.AdultOverride
	}
	if c.SectionOverride != nil {
		e.Section = *c.SectionOverride
	}
	switch {
	case e.Adult && !r.Adult:
		e.Enabled, e.Reason = false, "contenuti per adulti"
	case e.Country != "" && e.Country != CanonicalCountry(r.Home) && !keeps(r.Countries, e.Country):
		e.Enabled, e.Reason = false, "paese escluso: "+e.Country
	case c.LowRes && !r.LowRes:
		e.Enabled, e.Reason = false, "bassa qualità (doppioni)"
	}
	if c.Enabled != nil {
		e.Enabled = *c.Enabled
		if e.Enabled {
			e.Reason = ""
		} else {
			e.Reason = "esclusa a mano"
		}
	}
	return e
}

// keeps reports whether the kept countries include code, comparing codes
// so that "UK" saved by an older version matches "GB".
func keeps(list []string, code string) bool {
	for _, v := range list {
		if CanonicalCountry(v) == code {
			return true
		}
	}
	return false
}

// KindStats counts one kind of content.
type KindStats struct {
	Available int            `json:"available"` // offered by the providers
	Visible   int            `json:"visible"`   // kept by the selection
	Cards     int            `json:"cards"`     // after grouping variants
	Sections  map[string]int `json:"sections,omitempty"`
}

// Stats summarises a selection, per kind.
type Stats map[string]*KindStats

// Select decides, for every item, whether it is shown, which variant of
// each group is the primary one (a preferred subscription's, then best
// quality, then lowest id) and in
// which section a live channel goes. It mutates items and returns stats.
// Variants are grouped across subscriptions.
func Select(rules map[int64]Rules, cats []store.SelectionCategory, items []store.SelectionItem) Stats {
	type catKey struct {
		sub       int64
		kind, cat string
	}
	eff := make(map[catKey]Effective, len(cats))
	for _, c := range cats {
		eff[catKey{c.SubscriptionID, c.Kind, c.SourceID}] = EffectiveCategory(c, rules[c.SubscriptionID])
	}
	stats := Stats{}
	groups := map[string][]int{}
	for i := range items {
		it := &items[i]
		st := stats[it.Kind]
		if st == nil {
			st = &KindStats{Sections: map[string]int{}}
			stats[it.Kind] = st
		}
		st.Available++
		r := rules[it.SubscriptionID]
		e, known := eff[catKey{it.SubscriptionID, it.Kind, it.CategoryID}]
		if !known {
			e = EffectiveCategory(store.SelectionCategory{}, r) // uncategorised: home country
			if it.Kind == "live" {
				e.Section = string(SectionTV)
			}
		}
		it.Section, it.Adult = e.Section, e.Adult
		it.Visible = e.Enabled && !it.Hidden && yearAllowed(it, r)
		it.Primary, it.VariantCount = false, 1
		if !it.Visible {
			continue
		}
		st.Visible++
		key := it.Kind + "\x00" + it.GroupKey
		if it.GroupKey == "" {
			key = fmt.Sprintf("%s\x00#%d", it.Kind, it.ID)
		}
		groups[key] = append(groups[key], i)
	}
	for _, members := range groups {
		sort.Slice(members, func(a, b int) bool {
			x, y := items[members[a]], items[members[b]]
			if px, py := rules[x.SubscriptionID].Prefer, rules[y.SubscriptionID].Prefer; px != py {
				return px
			}
			if x.Quality != y.Quality {
				return x.Quality > y.Quality
			}
			return x.ID < y.ID
		})
		for _, m := range members {
			items[m].VariantCount = len(members)
		}
		best := &items[members[0]]
		best.Primary = true
		st := stats[best.Kind]
		st.Cards++
		if best.Section != "" {
			st.Sections[best.Section]++
		}
	}
	return stats
}

func yearAllowed(it *store.SelectionItem, r Rules) bool {
	min := 0
	switch it.Kind {
	case "movie":
		min = r.MovieMinYear
	case "series":
		min = r.SeriesMinYear
	}
	if min == 0 {
		return true
	}
	if it.Year == 0 {
		return !r.HideUnknownYear
	}
	return it.Year >= min
}
