package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
)

func TestTonightWindow(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	at := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, loc) }
	for _, c := range []struct{ now, from, to time.Time }{
		{at(31, 9, 25), at(31, 20, 0), at(32, 0, 30)},   // morning: the evening ahead, not the curling on now
		{at(31, 17, 59), at(31, 20, 0), at(32, 0, 30)},  // still before the evening
		{at(31, 18, 0), at(31, 18, 0), at(32, 0, 30)},   // from now on
		{at(31, 22, 45), at(31, 22, 45), at(32, 1, 45)}, // late: at least 3 hours
		{at(31, 1, 10), at(31, 1, 10), at(31, 4, 10)},   // after midnight: the night going on
	} {
		from, to := tonightWindow(c.now)
		if !from.Equal(c.from) || !to.Equal(c.to) {
			t.Errorf("%s: %s – %s, want %s – %s", c.now.Format("15:04"), from.Format("02 15:04"), to.Format("02 15:04"), c.from.Format("02 15:04"), c.to.Format("02 15:04"))
		}
	}
}

func TestHasPerson(t *testing.T) {
	for _, c := range []struct {
		people, person string
		want           bool
	}{
		{"Catherine Zeta-Jones, Michael Douglas", "Catherine Zeta-Jones", true},
		{"Catherine Zeta Jones", "Catherine Zeta-Jones", true},
		{"Brad Pitt, Eric Bana, Orlando Bloom Wolfgang Petersen", "Catherine Zeta-Jones", false},
		{"Leonardo DiCaprio Christopher Nolan", "Nolan", true},
		{"Kate Winslet", "Katherine Winslet", true}, // the given name may differ
		{"", "Brad Pitt", false},
	} {
		if got := hasPerson(c.people, c.person); got != c.want {
			t.Errorf("hasPerson(%q, %q) = %v", c.people, c.person, got)
		}
	}
}

func TestPeopleText(t *testing.T) {
	got := peopleText("Renée Zellweger, Richard Gere, Catherine Zeta-Jones", "Rob Marshall")
	if want := " renee zellweger , richard gere , catherine zeta jones , rob marshall "; got != want {
		t.Fatalf("peopleText = %q, want %q", got, want)
	}
	if peopleText("", "") != "" {
		t.Fatal("empty page")
	}
	for person, want := range map[string]string{"Catherine Zeta-Jones": "zeta jones", "catrin z jones": "z jones", "Nolan": "nolan"} {
		if got := personKey(person); got != want {
			t.Errorf("personKey(%q) = %q, want %q", person, got, want)
		}
	}
}

// "Il film di stasera su Canale 5", no time: the evening's main
// programmes on that channel (long, starting 20:30-23:30), the guide's
// film first when the sentence says "film"; short ones and the afternoon
// are not offered.
func TestOnceEvening(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	box, _ := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(st, box, nil)
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	at := func(h, m int) int64 { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).Unix() }
	progs := []store.GuideProgramme{
		{Channel: "c5", Start: at(16, 0), Stop: at(18, 0), Title: "Pomeriggio", Category: "Film"},
		{Channel: "c5", Start: at(20, 40), Stop: at(21, 20), Title: "Striscia", Category: "Varietà"},
		{Channel: "c5", Start: at(21, 20), Stop: at(23, 30), Title: "Grande Fratello", Category: "Reality"},
		{Channel: "c5", Start: at(23, 30), Stop: at(1, 30), Title: "Il film della notte", Category: "Film"},
	}
	if err := st.ReplaceGuide(ctx, "ext:1", []store.GuideChannel{{ID: "c5", NameKey: "canale5"}}, progs); err != nil {
		t.Fatal(err)
	}
	ch := store.TVChannel{ID: 7, Name: "Canale 5", GuideID: "c5"}
	titles := func(l []RuleMatch) []string {
		var out []string
		for _, m := range l {
			out = append(out, m.Title)
		}
		return out
	}
	date := day.Format("2006-01-02")
	got := titles(svc.onceCandidates(ctx, onceAsk{date: date, evening: true}, store.RecordingRule{}, false, ch, now))
	if strings.Join(got, ",") != "Grande Fratello" {
		t.Fatalf("tonight: %v", got)
	}
	// 23:30 is past prime time; Striscia is too short.
	got = titles(svc.onceCandidates(ctx, onceAsk{date: date, film: true}, store.RecordingRule{}, false, ch, now))
	if strings.Join(got, ",") != "Grande Fratello" {
		t.Fatalf("film tonight: %v", got)
	}
	progs[3].Start, progs[3].Stop = at(21, 25), at(23, 15)
	progs[2].Start = at(21, 15)
	if err := st.ReplaceGuide(ctx, "ext:1", []store.GuideChannel{{ID: "c5", NameKey: "canale5"}}, progs); err != nil {
		t.Fatal(err)
	}
	got = titles(svc.onceCandidates(ctx, onceAsk{date: date, evening: true, film: true}, store.RecordingRule{}, false, ch, now))
	if strings.Join(got, ",") != "Il film della notte,Grande Fratello" {
		t.Fatalf("the film first: %v", got)
	}
	// Neither a day, a time nor "stasera": nothing to record once.
	if got := svc.onceCandidates(ctx, onceAsk{}, store.RecordingRule{}, false, ch, now); got != nil {
		t.Fatalf("no day: %v", got)
	}
	for _, s := range []string{"resgistrami il film di quyesta sera su canale 5", "registra stasera", "tonight's film"} {
		if !eveningRE.MatchString(fold(s)) {
			t.Fatalf("evening not read: %q", s)
		}
	}
	if !filmRE.MatchString(fold("Film TV")) || filmRE.MatchString(fold("filmati")) {
		t.Fatal("film")
	}
}

// A lineup of 100 channels with a guide, each with the news at 19:55, a
// quiz at 20:30, the film at 21:30 and something late at 23:30: the
// evening list keeps every channel's film (the programme starting nearest
// prime time), not 60 quizzes, and later in the evening what is on and
// what comes next, two per channel. A favourite comes first.
func TestTonightManyChannels(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	box, _ := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	st, err := store.Open(filepath.Join(dir, "vodmax.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(st, box, nil)
	seal := func(int64) []byte { return []byte("x") }
	vpnID, _ := st.AddVPN(ctx, "v", "system", seal)
	sub, err := st.AddSubscription(ctx, store.Subscription{Name: "B", Kind: "xtream", VPNID: vpnID, Settings: json.RawMessage(`{}`)}, seal)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	at := func(h, m int) int64 { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).Unix() }
	var items []store.Item
	var guide []store.GuideChannel
	var progs []store.GuideProgramme
	for i := range 100 {
		id, name := fmt.Sprintf("ch%03d.it", i), fmt.Sprintf("Canale %03d", i)
		items = append(items, store.Item{Kind: "live", SourceID: fmt.Sprint(i), Title: name, DisplayTitle: name, CategoryID: "1",
			EPGID: id, GroupKey: "live:" + id, NameKey: id})
		guide = append(guide, store.GuideChannel{ID: id, NameKey: id})
		progs = append(progs,
			store.GuideProgramme{Channel: id, Start: at(19, 55), Stop: at(20, 30), Title: "TG"},
			store.GuideProgramme{Channel: id, Start: at(20, 30), Stop: at(21, 30), Title: "Quiz"},
			store.GuideProgramme{Channel: id, Start: at(21, 30), Stop: at(23, 30), Title: "Film " + name},
			store.GuideProgramme{Channel: id, Start: at(23, 30), Stop: at(24, 30), Title: "Notte"})
	}
	if _, err := st.ReplaceCatalog(ctx, sub, []string{"live"}, []store.Category{{Kind: "live", SourceID: "1", Name: "Italia", Country: "IT"}}, items, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetHomeCountry(ctx, "IT"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplySelection(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceGuide(ctx, "ext:1", guide, progs); err != nil {
		t.Fatal(err)
	}
	channels, err := st.TVChannels(ctx, 0, false, "", false, now)
	if err != nil || len(channels) != 100 {
		t.Fatalf("channels: %d %v", len(channels), err)
	}
	last := channels[len(channels)-1]
	if err := st.SetFavorite(ctx, 0, last.ID, true); err != nil {
		t.Fatal(err)
	}
	count := func(list []candidate) map[string]int {
		n := map[string]int{}
		for _, c := range list {
			n[strings.Fields(c.Title)[0]]++
		}
		return n
	}
	has := func(list []candidate, title string) bool {
		return slices.ContainsFunc(list, func(c candidate) bool { return c.Title == title })
	}
	// In the afternoon: the evening ahead, every channel's film.
	list, err := svc.tonightProgrammes(ctx, Viewer{}, time.Unix(at(15, 0), 0))
	if n := count(list); err != nil || len(list) != 60 || n["Film"] != 60 {
		t.Fatalf("afternoon: %d candidates %v %v", len(list), n, err)
	}
	if !has(list, "Film "+last.Name) {
		t.Fatal("afternoon: the favourite channel left out")
	}
	// At 21:40: the film on now and what follows, two per channel.
	list, err = svc.tonightProgrammes(ctx, Viewer{}, time.Unix(at(21, 40), 0))
	if n := count(list); err != nil || len(list) != 60 || n["Film"] != 30 || n["Notte"] != 30 {
		t.Fatalf("evening: %d candidates %v %v", len(list), n, err)
	}
	if !has(list, "Film "+last.Name) {
		t.Fatal("evening: the favourite channel left out")
	}
}
