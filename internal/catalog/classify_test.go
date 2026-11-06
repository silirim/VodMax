package catalog

import "testing"

func TestClassifyCategory(t *testing.T) {
	cases := []struct {
		kind, name string
		want       CategoryInfo
	}{
		{"live", "Live United States", CategoryInfo{Country: "United States", Section: SectionTV}},
		{"live", "Live Deutschland/Asterreich new", CategoryInfo{Country: "Deutschland/Asterreich", Section: SectionTV}},
		{"live", "International Sport Channels", CategoryInfo{Country: "Internazionale", Section: SectionSport}},
		{"live", "XXX Live", CategoryInfo{Adult: true, Section: SectionTV}},
		{"live", "SD (Connessioni Lente)", CategoryInfo{LowRes: true, Section: SectionTV}},
		{"live", "Italia 4K - FHD", CategoryInfo{Section: SectionTV}},
		{"live", "Campionato Serie A", CategoryInfo{Section: SectionSport}},
		{"live", "Dazn Locale", CategoryInfo{Section: SectionSport}},
		{"live", "F1 - MotoGP", CategoryInfo{Section: SectionSport}},
		{"live", "Radio", CategoryInfo{Section: SectionRadio}},
		{"live", "Ragazzi", CategoryInfo{Section: SectionTV}},
		{"live", "Live Calcio", CategoryInfo{Section: SectionSport}},
		{"live", "LIVE EVENTS", CategoryInfo{Section: SectionTV}},
		{"live", "TV Locali", CategoryInfo{Section: SectionTV}},
		{"movie", "XXX FULLHD Porn", CategoryInfo{Adult: true}},
		{"movie", "Commedia Italiana", CategoryInfo{}},
		{"movie", "Live Aid concerti", CategoryInfo{}}, // "Live" prefix only means a country for channels
	}
	for _, c := range cases {
		if got := ClassifyCategory(c.kind, c.name); got != c.want {
			t.Errorf("%s %q: %+v, atteso %+v", c.kind, c.name, got, c.want)
		}
	}
}

func TestQualityAndGroupKey(t *testing.T) {
	if Quality("Rai 1 4K", CategoryInfo{}) != 4 || Quality("Rai 1 FHD", CategoryInfo{}) != 3 ||
		Quality("Rai 1 HD", CategoryInfo{}) != 2 || Quality("Rai 1", CategoryInfo{LowRes: true}) != 1 ||
		Quality("Sky Sport SD", CategoryInfo{}) != 1 {
		t.Fatal("quality ranks")
	}
	same := [][]string{
		{"Rai 1 HD", "Rai 1 FHD", "Rai 1 4K", "RAI 1", "rai 1 [ITA]"},
		{"Città di Dio", "Citta di Dio"},
		{"Первый канал HD", "ПЕРВЫЙ КАНАЛ"},
	}
	for _, group := range same {
		for _, title := range group[1:] {
			if GroupKey("live", title, 0) != GroupKey("live", group[0], 0) {
				t.Errorf("%q e %q dovrebbero coincidere", title, group[0])
			}
		}
	}
	if GroupKey("live", "Rai 1 +1 HD", 0) == GroupKey("live", "Rai 1 HD", 0) {
		t.Error("+1 must stay a separate channel")
	}
	if GroupKey("movie", "Pinocchio", 1940) == GroupKey("movie", "Pinocchio", 2019) {
		t.Error("films with different years must not merge")
	}
	// Titles in other scripts keep their letters: they used to share "".
	keys := map[string]string{}
	for _, title := range []string{"Первый канал", "الجزيرة", "Россия 1", "Беларусь 1", "ΕΡΤ1", "हिन्दी", "Rai 1"} {
		k := GroupKey("live", title, 0)
		if other, ok := keys[k]; ok || k == "" {
			t.Errorf("%q and %q share the key %q", title, other, k)
		}
		keys[k] = title
	}
	if GroupKey("movie", "Москва слезам не верит", 1980) == GroupKey("movie", "Солярис", 1980) {
		t.Error("Cyrillic films of one year must not merge")
	}
}

// The keys of 0.6.6 and earlier, kept to carry what was saved with them.
func TestLegacyGroupKey(t *testing.T) {
	for _, c := range []struct {
		kind, title string
		year        int
		want        string
	}{
		{"live", "Россия 1", 0, "1"},
		{"live", "الجزيرة", 0, ""},
		{"movie", "Москва слезам не верит", 1980, "|1980"},
		{"live", "Rai 1 HD", 0, "rai 1"},
		{"movie", "Café Society", 2016, "cafe society|2016"},
	} {
		if got := LegacyGroupKey(c.kind, c.title, c.year); got != c.want {
			t.Errorf("%q: %q, want %q", c.title, got, c.want)
		}
	}
}
