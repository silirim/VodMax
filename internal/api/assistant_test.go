package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeModel is an OpenAI-compatible chat server with a scripted answer.
type fakeModel struct {
	*httptest.Server
	mu       sync.Mutex
	answer   func(system, user string) string
	requests []map[string]any
	noJSON   bool // answers 400 to response_format, like some servers
}

func newFakeModel(t *testing.T) *fakeModel {
	m := &fakeModel{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		m.mu.Lock()
		m.requests = append(m.requests, req)
		m.mu.Unlock()
		if _, ok := req["response_format"]; ok && m.noJSON {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"response_format not supported"}}`))
			return
		}
		msgs := req["messages"].([]any)
		system, user := "", ""
		for _, x := range msgs {
			mm := x.(map[string]any)
			if mm["role"] == "system" {
				system = mm["content"].(string)
			} else {
				user = mm["content"].(string)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": m.answer(system, user)}}}})
	}))
	t.Cleanup(m.Close)
	return m
}

func TestAssistant(t *testing.T) {
	e, _ := tvSetup(t)
	m := newFakeModel(t)
	if code, _ := e.do(t, "POST", "/api/v1/assistant/rule", map[string]string{"text": "la juve"}, true); code != 409 {
		t.Fatalf("off: %d", code)
	}
	if code, body := e.do(t, "GET", "/api/v1/assistant", nil, true); code != 200 || !strings.Contains(string(body), "false") {
		t.Fatalf("on: %d %s", code, body)
	}
	cfg := map[string]any{"enabled": true, "url": m.URL + "/v1/chat/completions", "model": "llama3", "key": "sk-test"}
	code, body := e.do(t, "PUT", "/api/v1/settings/assistant", cfg, true)
	if code != 200 || strings.Contains(string(body), "sk-test") || !strings.Contains(string(body), `"url":"`+m.URL+`/v1"`) {
		t.Fatalf("settings: %d %s", code, body)
	}
	m.answer = func(system, user string) string { return "I am llama3." }
	if code, body := e.do(t, "POST", "/api/v1/settings/assistant/test", nil, true); code != 200 || !strings.Contains(string(body), "llama3") {
		t.Fatalf("test: %d %s", code, body)
	}

	// A rule from a sentence: the model's channel name becomes the channel.
	m.noJSON = true
	m.answer = func(system, user string) string {
		if !strings.Contains(system, "Cielo") || !strings.HasPrefix(user, "registra le partite della Juve in diretta su Cielo") {
			return "?"
		}
		return "Ecco la regola:\n```json\n" + `{"name":"Juventus","all":["juventus"],"any":[],"none":["highlights"],"channel":"cielo","section":"","live_only":true,"keep":0,"explanation":"Registra le partite della Juventus in diretta su Cielo."}` + "\n```"
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/rule", map[string]string{"text": "registra le partite della Juve in diretta su Cielo"}, true)
	var p struct {
		Rule struct {
			Name      string
			All, None []string
			ChannelID int64 `json:"channel_id"`
			LiveOnly  bool  `json:"live_only"`
		}
		ChannelName string `json:"channel_name"`
		Explanation string
	}
	if code != 200 || json.Unmarshal(body, &p) != nil || p.Rule.Name != "Juventus" || p.Rule.All[0] != "juventus" || !p.Rule.LiveOnly ||
		p.Rule.ChannelID == 0 || p.ChannelName != "Cielo" || p.Explanation == "" {
		t.Fatalf("rule: %d %s", code, body)
	}
	if n := len(m.requests); n < 2 {
		t.Fatalf("no retry without response_format: %d requests", n)
	}
	// Not saved: the person checks it in the form.
	if _, body := e.do(t, "GET", "/api/v1/recording-rules", nil, true); strings.Contains(string(body), "Juventus") {
		t.Fatalf("saved: %s", body)
	}
	if len(p.Rule.All) == 0 || strings.Contains(string(body), `"once"`) {
		t.Fatalf("no day or time named, nothing to record once: %s", body)
	}

	// A day and a time: the programmes then on the channel named, whatever
	// their title, to record just once: first what starts nearest the time
	// ("Film della sera", in 20 minutes on Rai 1), then what is on air
	// (TG1). Without a title there is no rule to propose.
	// The model names minutes, not seconds: asking for 20 minutes from now
	// lands exactly on the film's start (TG1 then over) whenever the guide
	// was made in the first second of a minute or a minute ago. 19 minutes
	// is always inside TG1.
	at := time.Now().Add(19 * time.Minute)
	m.answer = func(system, user string) string {
		if !strings.Contains(system, time.Now().Format("2006-01-02")) {
			return "?"
		}
		return `{"name":"","all":[],"any":[],"none":[],"channel":"Rai 1","section":"","live_only":false,"keep":0,"date":"` + at.Format("2006-01-02") +
			`","time":"` + at.Format("15:04") + `","explanation":"Registra il film."}`
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/rule", map[string]string{"text": "registra il film di stasera su Rai 1"}, true)
	var o struct {
		Once []struct {
			ChannelID   int64 `json:"channel_id"`
			Title       string
			Start, Stop int64
		}
		RuleError string `json:"rule_error"`
	}
	if code != 200 || json.Unmarshal(body, &o) != nil || len(o.Once) != 2 || o.Once[0].Title != "Film della sera" || o.Once[0].ChannelID == 0 ||
		o.Once[1].Title != "TG1" {
		t.Fatalf("once: %d %s", code, body)
	}
	// "Just this once" is the guide's own recording request.
	rec := map[string]any{"channel_id": o.Once[0].ChannelID, "start": o.Once[0].Start, "stop": o.Once[0].Stop, "title": o.Once[0].Title, "from_guide": true}
	if code, body := e.do(t, "POST", "/api/v1/recordings", rec, true); code != 201 {
		t.Fatalf("record once: %d %s", code, body)
	}
	// A date the guide cannot have is not trusted.
	m.answer = func(system, user string) string {
		return `{"name":"Film","all":["film"],"channel":"Rai 1","date":"2031-01-01","time":"21:00","explanation":"."}`
	}
	if code, body := e.do(t, "POST", "/api/v1/assistant/rule", map[string]string{"text": "il film del 2031"}, true); code != 200 || strings.Contains(string(body), `"once"`) {
		t.Fatalf("far date: %d %s", code, body)
	}

	m.answer = func(system, user string) string { return "non so" }
	if code, _ := e.do(t, "POST", "/api/v1/assistant/rule", map[string]string{"text": "boh"}, true); code != 502 {
		t.Fatalf("unusable answer: %d", code)
	}

	// Tonight: only codes VodMax gave count. The test guide is around now,
	// which before 18:00 is not "tonight": then the pick is the film in
	// the list.
	_, body = e.do(t, "GET", "/api/v1/catalog/movie?limit=1", nil, true)
	var films struct{ Items []struct{ ID int64 } }
	if json.Unmarshal(body, &films) != nil || len(films.Items) == 0 {
		t.Fatalf("films: %s", body)
	}
	if code, body := e.do(t, "PUT", "/api/v1/mylist/"+jsonInt(films.Items[0].ID), map[string]bool{"on": true}, true); code >= 300 {
		t.Fatalf("my list: %d %s", code, body)
	}
	m.noJSON = false
	want := ""
	m.answer = func(system, user string) string {
		code, lib := "", ""
		for _, line := range strings.Split(user, "\n") {
			c := strings.TrimSpace(strings.SplitN(line, "|", 2)[0])
			if strings.Contains(line, "Film della sera") {
				code = c
			} else if regexp.MustCompile(`^L[0-9]+$`).MatchString(c) && lib == "" {
				lib = c
			}
		}
		want = "tv"
		if code == "" {
			code, want = lib, "movie"
		}
		return `{"intro":"Stasera un film.","picks":[{"code":"` + code + `","why":"Ti piacciono i film."},{"code":"T999","why":"inventato"},{"code":"X1","why":"?"}]}`
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "qualcosa di leggero"}, true)
	var tn struct {
		Intro string
		Picks []struct {
			Kind, Title, Why, Channel string
			ChannelID                 int64 `json:"channel_id"`
			Start                     int64
		}
	}
	if code != 200 || json.Unmarshal(body, &tn) != nil || len(tn.Picks) != 1 || tn.Intro == "" || tn.Picks[0].Kind != want {
		t.Fatalf("tonight: %d %s", code, body)
	}
	if p := tn.Picks[0]; want == "tv" && (p.Title != "Film della sera" || p.Channel == "" || p.ChannelID == 0 || p.Start == 0) {
		t.Fatalf("tonight on TV: %s", body)
	}
	last := m.requests[len(m.requests)-1]
	if msgs := last["messages"].([]any); !strings.Contains(msgs[0].(map[string]any)["content"].(string), "qualcosa di leggero") {
		t.Fatalf("wish not passed: %v", msgs[0])
	}
	// The same question again within minutes: from the cache.
	n := len(m.requests)
	e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "qualcosa di leggero"}, true)
	if len(m.requests) != n {
		t.Fatal("not cached")
	}

	// A request for something specific: the model names titles, VodMax
	// looks for them in the catalog and offers only the ones it has.
	_, body = e.do(t, "GET", "/api/v1/catalog/movie?limit=50", nil, true)
	var all struct {
		Items []struct {
			ID           int64
			DisplayTitle string `json:"display_title"`
		}
	}
	json.Unmarshal(body, &all)
	var asked struct {
		ID           int64
		DisplayTitle string `json:"display_title"`
	}
	for _, it := range all.Items {
		if it.ID != films.Items[0].ID && it.DisplayTitle != "" {
			asked = it
			break
		}
	}
	if asked.ID == 0 {
		t.Fatalf("no second film: %s", body)
	}
	m.answer = func(system, user string) string {
		if strings.Contains(system, "home catalog") {
			return `{"titles":[{"title":` + strconv.Quote(asked.DisplayTitle) + `},{"title":"Un film che non c'è"}]}`
		}
		for _, line := range strings.Split(user, "\n") {
			if strings.Contains(line, "(matches the request)") {
				return `{"intro":"Eccolo.","picks":[{"code":"` + strings.TrimSpace(strings.SplitN(line, "|", 2)[0]) + `","why":"L'hai chiesto tu."}]}`
			}
		}
		return `{"intro":"Non c'è.","picks":[]}`
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "quel film con quell'attore"}, true)
	tn.Picks = nil
	if code != 200 || json.Unmarshal(body, &tn) != nil || len(tn.Picks) != 1 || tn.Picks[0].Title != asked.DisplayTitle {
		t.Fatalf("asked for a title: %d %s", code, body)
	}
	// A person, misspelled: the model names titles (one wrong), VodMax
	// keeps only those whose detail page has that person, and drops the
	// "alternatives" the model adds anyway.
	m.answer = func(system, user string) string {
		if strings.Contains(system, "home catalog") {
			return `{"about":"person","person":"John Cusack","titles":[{"title":"1408","year":2007},{"title":"Psycho"}]}`
		}
		var picks []string
		for _, line := range strings.Split(user, "\n") {
			if c := strings.TrimSpace(strings.SplitN(line, "|", 2)[0]); regexp.MustCompile(`^[LT][0-9]+$`).MatchString(c) {
				picks = append(picks, `{"code":"`+c+`","why":"C'è lui."}`)
			}
		}
		return `{"intro":"Ecco.","picks":[` + strings.Join(picks, ",") + `]}`
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "un film con jon cusak"}, true)
	tn.Picks = nil
	if code != 200 || json.Unmarshal(body, &tn) != nil || len(tn.Picks) != 1 || tn.Picks[0].Title != "1408" {
		t.Fatalf("a person: %d %s", code, body)
	}
	if last := m.requests[len(m.requests)-1]; !strings.Contains(fmt.Sprint(last["messages"]), "(with John Cusack)") {
		t.Fatalf("checked title not marked: %v", last["messages"])
	}
	// Psycho has no cast on its page: it cannot be checked, so it is not
	// offered as a film with that actor.
	m.answer = func(system, user string) string {
		if strings.Contains(system, "home catalog") {
			return `{"about":"person","person":"Anthony Perkins","titles":[{"title":"Psycho","year":1960}]}`
		}
		for _, line := range strings.Split(user, "\n") {
			if strings.Contains(line, "Psycho") {
				return `{"intro":"Psycho!","picks":[{"code":"` + strings.TrimSpace(strings.SplitN(line, "|", 2)[0]) + `","why":"C'è lui."}]}`
			}
		}
		return `{"intro":"Niente.","picks":[]}`
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "un film con anthony perkins"}, true)
	tn.Picks, tn.Intro = nil, ""
	if code != 200 || json.Unmarshal(body, &tn) != nil || len(tn.Picks) != 0 || tn.Intro == "Psycho!" {
		t.Fatalf("unchecked title: %d %s", code, body)
	}

	// A genre: the model gives its words; the provider's category with that
	// name ("Horror") brings its titles, marked for the model, also ones it
	// did not name.
	var promptGenre string
	m.answer = func(system, user string) string {
		if strings.Contains(system, "home catalog") {
			return `{"about":"mood","person":"","genres":["horror","paura"],"titles":[]}`
		}
		promptGenre = user
		for _, line := range strings.Split(user, "\n") {
			if strings.Contains(line, "Psycho") {
				return `{"intro":"Un horror.","picks":[{"code":"` + strings.TrimSpace(strings.SplitN(line, "|", 2)[0]) + `","why":"Fa paura."}]}`
			}
		}
		return `{"intro":"Niente.","picks":[]}`
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "voglio vedere un film horror"}, true)
	tn.Picks, tn.Intro = nil, ""
	if code != 200 || json.Unmarshal(body, &tn) != nil || len(tn.Picks) != 1 || tn.Picks[0].Title != "Psycho" {
		t.Fatalf("a genre: %d %s\n%s", code, body, promptGenre)
	}
	if !strings.Contains(promptGenre, "(genre: Horror)") || !strings.Contains(promptGenre, "1408") {
		t.Fatalf("genre titles not offered: %s", promptGenre)
	}

	// Nothing fits: a sentence, not an error.
	m.answer = func(system, user string) string {
		if strings.Contains(system, "home catalog") {
			return `{"titles":[]}`
		}
		return `{"intro":"Stasera niente del genere.","picks":[]}`
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "un documentario sui pinguini"}, true)
	tn.Picks, tn.Intro = nil, ""
	if code != 200 || json.Unmarshal(body, &tn) != nil || len(tn.Picks) != 0 || tn.Intro != "Stasera niente del genere." || !strings.Contains(string(body), `"picks":[]`) {
		t.Fatalf("nothing fits: %d %s", code, body)
	}
	// Only codes the model made up: its intro describes picks that are not
	// there, VodMax says nothing fits instead.
	m.answer = func(system, user string) string {
		if strings.Contains(system, "home catalog") {
			return `{"titles":[]}`
		}
		return `{"intro":"Ecco tre film perfetti per stasera.","picks":[{"code":"T999","why":"?"},{"code":"L42","why":"?"},{"code":"","why":"?"}]}`
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "tre film"}, true)
	tn.Picks, tn.Intro = nil, ""
	if code != 200 || json.Unmarshal(body, &tn) != nil || len(tn.Picks) != 0 || !strings.HasPrefix(tn.Intro, "Niente di adatto") {
		t.Fatalf("made-up codes: %d %s", code, body)
	}
}

// The first step of a precise request ("a film with…") reads what is
// asked. An answer VodMax cannot read is an error, not a request with
// nothing to check: otherwise a quiz show passed as a film with that
// actor. A year written as a string still reads.
func TestAssistantRequestUnreadable(t *testing.T) {
	e, _ := tvSetup(t)
	m := newFakeModel(t)
	cfg := map[string]any{"enabled": true, "url": m.URL + "/v1", "model": "llama3", "key": "sk-test"}
	if code, body := e.do(t, "PUT", "/api/v1/settings/assistant", cfg, true); code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	everything := func(user string) string {
		var picks []string
		for _, line := range strings.Split(user, "\n") {
			if c := strings.TrimSpace(strings.SplitN(line, "|", 2)[0]); regexp.MustCompile(`^[LT][0-9]+$`).MatchString(c) {
				picks = append(picks, `{"code":"`+c+`","why":"C'è lui."}`)
			}
		}
		return `{"intro":"Ecco un film con John Cusack.","picks":[` + strings.Join(picks, ",") + `]}`
	}
	m.answer = func(system, user string) string {
		if strings.Contains(system, "home catalog") {
			return "Sure! John Cusack starred in many films."
		}
		return everything(user)
	}
	code, body := e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "un film con john cusack"}, true)
	if code != 502 || len(m.requests) != 1 {
		t.Fatalf("unreadable first answer: %d %s, %d requests", code, body, len(m.requests))
	}
	m.answer = func(system, user string) string {
		if strings.Contains(system, "home catalog") {
			return `{"about":"person","person":"John Cusack","titles":[{"title":"1408","year":"2007"}]}`
		}
		return everything(user)
	}
	code, body = e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "un film con john cusack"}, true)
	var tn struct{ Picks []struct{ Title string } }
	if code != 200 || json.Unmarshal(body, &tn) != nil || len(tn.Picks) != 1 || tn.Picks[0].Title != "1408" {
		t.Fatalf("year as a string: %d %s", code, body)
	}
}

// The model's service out of credit or refusing the key is not a failure
// of VodMax: 502 with a text for people, nothing in the log as an error,
// and never the service's own text about the key.
func TestAssistantServiceRefuses(t *testing.T) {
	e, _ := tvSetup(t)
	var mu sync.Mutex
	status, text := 429, `{"error":{"message":"You exceeded your current quota"}}`
	m := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.WriteHeader(status)
		w.Write([]byte(text))
	}))
	defer m.Close()
	cfg := map[string]any{"enabled": true, "url": m.URL + "/v1", "model": "llama3", "key": "sk-test"}
	if code, body := e.do(t, "PUT", "/api/v1/settings/assistant", cfg, true); code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	set := func(code int, body string) {
		mu.Lock()
		status, text = code, body
		mu.Unlock()
	}
	code, body := e.do(t, "POST", "/api/v1/assistant/rule", map[string]string{"text": "registra la juve"}, true)
	if code != 502 || !strings.Contains(string(body), "credito") {
		t.Fatalf("no credit: %d %s", code, body)
	}
	set(401, `{"error":{"message":"Incorrect API key provided: sk-proj-****abcd"}}`)
	code, body = e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": ""}, true)
	if code != 502 || strings.Contains(string(body), "abcd") || !strings.Contains(string(body), "chiave API") {
		t.Fatalf("key refused: %d %s", code, body)
	}
	set(500, `{"error":{"message":"overloaded"}}`)
	code, body = e.do(t, "POST", "/api/v1/assistant/rule", map[string]string{"text": "registra la juve"}, true)
	if code != 502 || !strings.Contains(string(body), "500 overloaded") {
		t.Fatalf("overloaded: %d %s", code, body)
	}
}

// The people index (decision 0036): opened pages and the night fill it;
// the assistant finds every title of a person in it.
// A detail page that cannot be read is noted without people and the
// night goes on; a provider that does not answer stops the index until
// the next night, without asking again every 10 minutes.
func TestPeopleIndexSkipsUnreadablePages(t *testing.T) {
	assistantOn := func(e *env) {
		m := newFakeModel(t)
		e.svc.SetPeoplePause(0)
		cfg := map[string]any{"enabled": true, "url": m.URL + "/v1", "model": "llama3", "key": "sk-test"}
		if code, body := e.do(t, "PUT", "/api/v1/settings/assistant", cfg, true); code != 200 {
			t.Fatalf("settings: %d %s", code, body)
		}
	}
	night := func() bool { return true }

	e, p := tvSetup(t)
	assistantOn(e)
	p.mu.Lock()
	p.badSeries = true // Twin Peaks
	p.mu.Unlock()
	if n := e.svc.FillPeople(context.Background(), slog.Default(), night); n != 3 {
		t.Fatalf("pages after an unreadable one: %d", n)
	}
	if again := e.svc.FillPeople(context.Background(), slog.Default(), night); again != 0 {
		t.Fatalf("unreadable page asked again tonight: %d", again)
	}

	e, p = tvSetup(t)
	assistantOn(e)
	p.mu.Lock()
	p.detailsDown = true
	p.mu.Unlock()
	if n := e.svc.FillPeople(context.Background(), slog.Default(), night); n != 0 {
		t.Fatalf("pages with the provider down: %d", n)
	}
	p.mu.Lock()
	p.detailsDown, p.detailHits = false, 0
	p.mu.Unlock()
	if n := e.svc.FillPeople(context.Background(), slog.Default(), night); n != 0 || p.detailHits != 0 {
		t.Fatalf("asked again the same night: %d pages, %d requests", n, p.detailHits)
	}
}

func TestPeopleIndex(t *testing.T) {
	e, _ := tvSetup(t)
	m := newFakeModel(t)
	e.svc.SetPeoplePause(0)
	cfg := map[string]any{"enabled": true, "url": m.URL + "/v1", "model": "llama3", "key": "sk-test"}
	if code, body := e.do(t, "PUT", "/api/v1/settings/assistant", cfg, true); code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	coverage := func() (known, total int) {
		_, body := e.do(t, "GET", "/api/v1/settings/assistant", nil, true)
		var v struct{ People struct{ Known, Total int } }
		json.Unmarshal(body, &v)
		return v.People.Known, v.People.Total
	}
	if known, total := coverage(); known != 0 || total == 0 {
		t.Fatalf("coverage before: %d of %d", known, total)
	}
	n := e.svc.FillPeople(context.Background(), slog.Default(), func() bool { return true })
	if known, total := coverage(); n != total || known != 1 { // only 1408 has a cast
		t.Fatalf("after the night: %d pages, %d of %d known", n, known, total)
	}
	if again := e.svc.FillPeople(context.Background(), slog.Default(), func() bool { return true }); again != 0 {
		t.Fatalf("pages read twice: %d", again)
	}
	// The model only understands who; the index knows the titles.
	m.answer = func(system, user string) string {
		if strings.Contains(system, "home catalog") {
			return `{"about":"person","person":"John Cusack","titles":[]}`
		}
		for _, line := range strings.Split(user, "\n") {
			if strings.Contains(line, "(with John Cusack)") {
				return `{"intro":"Eccolo.","picks":[{"code":"` + strings.TrimSpace(strings.SplitN(line, "|", 2)[0]) + `","why":"C'è lui."}]}`
			}
		}
		return `{"intro":"Niente.","picks":[]}`
	}
	code, body := e.do(t, "POST", "/api/v1/assistant/tonight", map[string]string{"wish": "un film con jon cusak"}, true)
	var tn struct{ Picks []struct{ Title string } }
	if code != 200 || json.Unmarshal(body, &tn) != nil || len(tn.Picks) != 1 || tn.Picks[0].Title != "1408" {
		t.Fatalf("from the index: %d %s", code, body)
	}
}

// A provider that does not answer at night is not "no cast": nothing is
// recorded and the night stops.
func TestPeopleIndexProviderDown(t *testing.T) {
	e, p := tvSetup(t)
	m := newFakeModel(t)
	e.svc.SetPeoplePause(0)
	cfg := map[string]any{"enabled": true, "url": m.URL + "/v1", "model": "llama3", "key": "sk-test"}
	e.do(t, "PUT", "/api/v1/settings/assistant", cfg, true)
	p.Close()
	if n := e.svc.FillPeople(context.Background(), slog.Default(), func() bool { return true }); n != 0 {
		t.Fatalf("%d pages recorded with the provider down", n)
	}
	// Off, the assistant does not read pages at night.
	cfg["enabled"] = false
	e.do(t, "PUT", "/api/v1/settings/assistant", cfg, true)
	if n := e.svc.FillPeople(context.Background(), slog.Default(), func() bool { return true }); n != 0 {
		t.Fatalf("%d pages read with the assistant off", n)
	}
}
