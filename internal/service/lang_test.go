package service

import "testing"

// Texts saved in Italian (a rule's note, a sync error) are shown in the
// reader's language, values kept.
func TestTranslateText(t *testing.T) {
	cases := map[string]string{
		"3 in programma": "3 scheduled",
		"2 in programma (1 nuove), 3 repliche saltate":                "2 scheduled (1 new), 3 reruns skipped",
		"il provider ha smesso di mandare dati":                       "the provider stopped sending data",
		"registrazione parziale: interrotta da un riavvio del server": "partial recording: interrupted by a server restart",
		"un testo che nessuno conosce":                                "un testo che nessuno conosce",
	}
	for in, want := range cases {
		if got := TranslateText("en", in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if got := TranslateText("it", "3 in programma"); got != "3 in programma" {
		t.Errorf("Italian changed: %q", got)
	}
}
