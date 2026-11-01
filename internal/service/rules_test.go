package service

import (
	"testing"

	"github.com/silirim/vodmax/internal/store"
)

func TestRuleMatching(t *testing.T) {
	juve := store.RuleSpec{All: []string{"juve"}, None: []string{"highlights", "replica"}}
	f1 := store.RuleSpec{All: []string{"formula 1"}, Any: []string{"gara", "qualifiche"}}
	inter := store.RuleSpec{All: []string{"inter"}}
	onF1 := store.RuleSpec{All: []string{"F1"}, Any: []string{"qualifiche", "gara", "libere"}, Channel: "sky sport f1"}
	cases := []struct {
		spec  store.RuleSpec
		title string
		group string
		want  bool
	}{
		{juve, "Serie A: Juventus - Inter", "", true},
		{juve, "JUVENTÙS-Milan (diretta)", "", true},
		{juve, "Highlights: Juventus - Inter", "", false},
		{juve, "Il Mondo Juve - replica", "", false},
		{juve, "Napoli - Roma", "", false},
		{f1, "Formula 1 - GP d'Italia: Gara", "", true},
		{f1, "Formula 1 - GP d'Italia - Qualifiche", "", true},
		{f1, "Formula 1 - Prove libere 1", "", false},
		{f1, "Formula 10 - Gara", "", false},      // a phrase matches whole words
		{f1, "F1 GP del Messico: Gara", "", true}, // another name of the same thing
		// One word is that word, not every word that starts with it.
		{inter, "Inter - Milan", "", true},
		{inter, "Internazionali d'Italia: Tennis", "", false},
		{inter, "Intervista al presidente", "", false},
		{store.RuleSpec{All: []string{"film"}}, "Grandi films", "", true},
		// On the rule's channel the channel's name counts: "F1" on Sky Sport F1.
		{onF1, "GP Messico: Qualifiche", "sky sport f1", true},
		{onF1, "GP Messico: Qualifiche", "sky sport 1", false},
		{onF1, "Inside the Team", "sky sport f1", false},
	}
	for _, c := range cases {
		if got := ruleMatches(c.spec, store.Programme{Title: c.title}, c.group); got != c.want {
			t.Errorf("%v on %q = %v", c.spec, c.title, got)
		}
	}
}
