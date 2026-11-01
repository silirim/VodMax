package api

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

type ruleMatch struct {
	Channel, Title, State, Note string
}

func preview(t *testing.T, e *env, body map[string]any) []ruleMatch {
	t.Helper()
	code, out := e.do(t, "POST", "/api/v1/recording-rules/preview", body, true)
	var list []ruleMatch
	if code != 200 || json.Unmarshal(out, &list) != nil {
		t.Fatalf("preview: %d %s", code, out)
	}
	return list
}

func TestRecordingRules(t *testing.T) {
	e, _ := tvSetup(t)
	if code, _ := e.do(t, "POST", "/api/v1/recording-rules", map[string]any{"name": "!!", "all": []string{" "}}, true); code != 400 {
		t.Fatal("rule without words accepted")
	}
	// Without words the name is what to look for.
	if p := preview(t, e, map[string]any{"name": "film della"}); len(p) != 1 || p[0].Title != "Film della sera" {
		t.Fatalf("name as words: %+v", p)
	}
	film := map[string]any{"name": "Film", "all": []string{"film"}, "none": []string{"replica"}}
	if p := preview(t, e, film); len(p) != 1 || p[0].Channel != "Rai 1" || p[0].State != "new" {
		t.Fatalf("preview: %+v", p)
	}
	code, body := e.do(t, "POST", "/api/v1/recording-rules", film, true)
	var rule struct {
		ID       int64
		LastNote string `json:"last_note"`
	}
	if code != 201 || json.Unmarshal(body, &rule) != nil || !strings.Contains(rule.LastNote, "1 nuove") {
		t.Fatalf("add: %d %s", code, body)
	}
	recs := func() []struct {
		ID     int64
		Title  string
		RuleID int64 `json:"rule_id"`
	} {
		_, body := e.do(t, "GET", "/api/v1/recordings", nil, true)
		var list []struct {
			ID     int64
			Title  string
			RuleID int64 `json:"rule_id"`
		}
		json.Unmarshal(body, &list)
		return list
	}
	if l := recs(); len(l) != 1 || l[0].Title != "Film della sera" || l[0].RuleID != rule.ID {
		t.Fatalf("recordings: %+v", l)
	}
	// Applying again adds nothing.
	id := strconv.FormatInt(rule.ID, 10)
	e.do(t, "PUT", "/api/v1/recording-rules/"+id, film, true)
	if l := recs(); len(l) != 1 {
		t.Fatalf("duplicated: %+v", l)
	}
	// One connection: a programme on another channel at the same time.
	if p := preview(t, e, map[string]any{"name": "Case", "all": []string{"sogno"}}); len(p) != 1 || p[0].State != "conflict" ||
		!strings.Contains(p[0].Note, "Film della sera") {
		t.Fatalf("conflict preview: %+v", p)
	}
	// Cancelled by hand: not scheduled again.
	e.do(t, "DELETE", "/api/v1/recordings/"+strconv.FormatInt(recs()[0].ID, 10), nil, true)
	if l := recs(); len(l) != 0 {
		t.Fatalf("cancelled still listed: %+v", l)
	}
	_, body = e.do(t, "PUT", "/api/v1/recording-rules/"+id, film, true)
	if l := recs(); len(l) != 0 || !strings.Contains(string(body), "1 annullate a mano") {
		t.Fatalf("rescheduled after cancel: %+v %s", l, body)
	}
	if p := preview(t, e, film); len(p) != 1 || p[0].State != "cancelled" {
		t.Fatalf("cancelled preview: %+v", p)
	}
	// Live only: the guide says the match was already shown.
	match := map[string]any{"name": "Juve", "all": []string{"juventus"}}
	if p := preview(t, e, match); len(p) != 1 || p[0].State != "new" {
		t.Fatalf("match preview: %+v", p)
	}
	match["live_only"] = true
	if p := preview(t, e, match); len(p) != 1 || p[0].State != "rerun" || !strings.Contains(p[0].Note, "già trasmessa") {
		t.Fatalf("live-only preview: %+v", p)
	}
	if code, body := e.do(t, "POST", "/api/v1/recording-rules", match, true); code != 201 || !strings.Contains(string(body), "1 repliche saltate") ||
		!strings.Contains(string(body), `"live_only":true`) {
		t.Fatalf("live-only rule: %d %s", code, body)
	}
	if l := recs(); len(l) != 0 {
		t.Fatalf("rerun recorded: %+v", l)
	}
	// Kids profiles cannot manage rules.
	kid := addKids(t, e)
	if code, _ := e.call(t, "GET", "/api/v1/recording-rules", nil, kid.device, kid.profile); code != 403 {
		t.Fatalf("kids rules: %d", code)
	}
	// Space and limits.
	if code, body := e.do(t, "GET", "/api/v1/recordings/space", nil, true); code != 200 || !strings.Contains(string(body), "free_bytes") {
		t.Fatalf("space: %d %s", code, body)
	}
	if code, _ := e.do(t, "PUT", "/api/v1/settings/recordings", map[string]int{"max_gb": -1}, true); code != 400 {
		t.Fatal("negative limit accepted")
	}
	// Deleting the rule removes its markers.
	if code, _ := e.do(t, "DELETE", "/api/v1/recording-rules/"+id, nil, true); code != 204 {
		t.Fatal("delete rule")
	}
}

type kidsLogin struct{ device, profile string }

func addKids(t *testing.T, e *env) kidsLogin {
	t.Helper()
	e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": "9753"}, true) // needed with other profiles
	_, body := e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "Bimbi", "kind": "kids"}, true)
	var p struct{ ID int64 }
	json.Unmarshal(body, &p)
	dev := pair(t, e, "Tablet", nil)
	_, tok := selectProfile(t, e, dev, p.ID, "")
	return kidsLogin{dev, tok}
}

// Sospendi / Riattiva send the rule back with its channel's key: the rule
// keeps recording only on its channel.
func TestRuleSuspendKeepsChannel(t *testing.T) {
	e, _ := tvSetup(t)
	rai := channels(t, e, "?section=tv")[0]
	code, body := e.do(t, "POST", "/api/v1/recording-rules", map[string]any{"name": "Film", "all": []string{"film"}, "channel_id": rai.ID}, true)
	var rule struct {
		ID   int64
		Spec struct{ Channel string }
	}
	if code != 201 || json.Unmarshal(body, &rule) != nil || rule.Spec.Channel == "" {
		t.Fatalf("add: %d %s", code, body)
	}
	id := strconv.FormatInt(rule.ID, 10)
	for _, enabled := range []bool{false, true} {
		toggle := map[string]any{"name": "Film", "all": []string{"film"}, "channel_id": 0, "channel": rule.Spec.Channel, "enabled": enabled}
		if code, body := e.do(t, "PUT", "/api/v1/recording-rules/"+id, toggle, true); code != 200 {
			t.Fatalf("toggle %v: %d %s", enabled, code, body)
		}
	}
	_, body = e.do(t, "GET", "/api/v1/recording-rules", nil, true)
	if !strings.Contains(string(body), `"channel":"`+rule.Spec.Channel+`"`) {
		t.Fatalf("channel lost: %s", body)
	}
	bad := map[string]any{"name": "Film", "all": []string{"film"}, "channel": "nessun canale"}
	if code, _ := e.do(t, "PUT", "/api/v1/recording-rules/"+id, bad, true); code != 400 {
		t.Fatalf("unknown channel key accepted: %d", code)
	}
}

// A programme the guide moved: the rule's old recording goes, the new time
// is scheduled. A rule moved to another channel drops the old channel's.
func TestRuleDropsStaleRecordings(t *testing.T) {
	e, _ := tvSetup(t)
	film := map[string]any{"name": "Film", "all": []string{"film"}, "none": []string{"replica"}}
	code, body := e.do(t, "POST", "/api/v1/recording-rules", film, true)
	var rule struct{ ID int64 }
	if code != 201 || json.Unmarshal(body, &rule) != nil {
		t.Fatalf("add: %d %s", code, body)
	}
	id := strconv.FormatInt(rule.ID, 10)
	list := func() []struct {
		Title       string
		Start, Stop int64
	} {
		_, body := e.do(t, "GET", "/api/v1/recordings", nil, true)
		var l []struct {
			Title       string
			Start, Stop int64
		}
		json.Unmarshal(body, &l)
		return l
	}
	before := list()
	if len(before) != 1 {
		t.Fatalf("recordings: %+v", before)
	}
	// As if the guide had the film three hours later when it was scheduled.
	db, err := sql.Open("sqlite", e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE recording SET start = start + 10800, stop = stop + 10800`); err != nil {
		t.Fatal(err)
	}
	e.do(t, "PUT", "/api/v1/recording-rules/"+id, film, true)
	if after := list(); len(after) != 1 || after[0].Start != before[0].Start {
		t.Fatalf("after the guide moved the film: %+v (was %+v)", after, before)
	}

	// The rule on another channel: nothing left on the first one.
	other := channels(t, e, "?section=tv")[1]
	film["channel_id"] = other.ID
	e.do(t, "PUT", "/api/v1/recording-rules/"+id, film, true)
	for _, r := range list() {
		if r.Start == before[0].Start {
			t.Fatalf("recording on the old channel kept: %+v", list())
		}
	}
}
