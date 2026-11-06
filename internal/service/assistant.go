package service

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silirim/vodmax/internal/store"
)

// The assistant: a language model chosen by the admin, in one of three
// ways: built in (VodMax downloads and runs it, assistant_local.go), a
// service on the internet with an API key (assistant_online.go), or the
// admin's own server (Ollama, LM Studio...) through the OpenAI-compatible
// chat API. It turns a sentence into a recording rule
// and picks something to watch tonight. Off by default. The model only
// proposes: a rule is filled in for the person to check, and tonight's
// picks can only be programmes and titles VodMax gave it, by code.

const (
	assistantKey       = "assistant"
	assistantSecretKey = "assistant_key"
	assistantKeyFor    = "assistant_key_for" // where the key was given for
	assistantTimeout   = 5 * time.Minute     // a small model on a processor reads ~50 tokens a second
	tonightCache       = 20 * time.Minute
)

var (
	ErrAssistantOff  = errors.New("l'assistente non è configurato: Impostazioni → Assistente")
	ErrAssistant     = errors.New("l'assistente ha dato una risposta che VodMax non sa usare: riprova o cambia modello")
	ErrAssistantSlow = errors.New("l'assistente non ha risposto in tempo: riprova o scegli un modello più veloce")
)

// Time limits (decision 0050). A whole question (a rule, tonight) gets
// one: every call to the model, the engine starting, the detail pages.
// An online service answers in seconds, so a call that hangs is not
// waited for; a model in the house on a processor needs minutes. A proxy
// in front of VodMax may wait less (docs/install.md). Variables for tests.
var (
	onlineLimit       = 3 * time.Minute
	homeLimit         = 6 * time.Minute
	onlineCallTimeout = time.Minute
)

func assistantLimit(a AssistantSettings) time.Duration {
	if a.Mode == "online" {
		return onlineLimit
	}
	return homeLimit
}

// withLimit bounds a question or a call; done, deferred, turns the error
// of anything the limit cut into ErrAssistantSlow.
func withLimit(ctx context.Context, limit time.Duration, err *error) (context.Context, func()) {
	ctx, cancel := context.WithTimeoutCause(ctx, limit, ErrAssistantSlow)
	return ctx, func() {
		if *err != nil && errors.Is(context.Cause(ctx), ErrAssistantSlow) {
			*err = ErrAssistantSlow
		}
		cancel()
	}
}

// AssistantUpstream is a failure of the model's service: unreachable,
// credit run out, overloaded, a key refused. Not a fault of VodMax: the
// API answers 502 and the log has no error for every question asked.
type AssistantUpstream struct{ Err error }

func (e AssistantUpstream) Error() string { return e.Err.Error() }
func (e AssistantUpstream) Unwrap() error { return e.Err }

// AssistantSettings: Mode is "local" (built in: Model is one of
// localModels), "online" (Provider, Model) or "server" (URL, the base of
// the API, for example http://ollama:11434/v1, and Model; also the empty
// mode of older versions). The key, when needed, is kept apart, sealed.
type AssistantSettings struct {
	Enabled  bool   `json:"enabled"`
	Mode     string `json:"mode,omitempty"`
	Provider string `json:"provider,omitempty"`
	URL      string `json:"url"`
	Model    string `json:"model"`
}

type assistantState struct {
	mu      sync.Mutex
	tonight map[string]tonightEntry // profile + wish
	client  *http.Client            // tests
	local   localState

	peoplePause *time.Duration // tests
	peopleHalt  time.Time      // the provider did not answer the people index
}

type tonightEntry struct {
	at  time.Time
	out Tonight
}

func (s *Service) AssistantSettings(ctx context.Context) (AssistantSettings, bool, error) {
	var a AssistantSettings
	if err := s.st.Setting(ctx, assistantKey, &a); err != nil {
		return a, false, err
	}
	key, err := s.assistantSecret(ctx, a)
	return a, key != "", err
}

// keyScope names where a key is sent: a service, or a server address. A
// key given for one never goes to another (an online key must not reach
// any address typed as "your server", nor one service's key another).
func keyScope(a AssistantSettings) string {
	switch a.Mode {
	case "online":
		return "online:" + a.Provider
	case "local":
		return ""
	}
	return "server:" + a.URL
}

// assistantSecret returns the stored key if it was given for where a
// sends its requests, "" otherwise.
func (s *Service) assistantSecret(ctx context.Context, a AssistantSettings) (string, error) {
	key, err := s.secretSetting(ctx, assistantSecretKey)
	if err != nil || key == "" {
		return "", err
	}
	scope, err := s.assistantKeyScope(ctx)
	if err != nil || scope != keyScope(a) {
		return "", err
	}
	return key, nil
}

// assistantKeyScope reads where the key was given for. Keys saved before
// 0.6.0-beta.7 have no scope: they belong to the settings saved with them.
func (s *Service) assistantKeyScope(ctx context.Context) (string, error) {
	var scope string
	if err := s.st.Setting(ctx, assistantKeyFor, &scope); err != nil || scope != "" {
		return scope, err
	}
	var saved AssistantSettings
	if err := s.st.Setting(ctx, assistantKey, &saved); err != nil {
		return "", err
	}
	scope = keyScope(saved)
	return scope, s.st.SetSetting(ctx, assistantKeyFor, scope)
}

// AssistantInput changes the settings; Key nil keeps the current one, ""
// removes it.
type AssistantInput struct {
	Enabled  bool    `json:"enabled"`
	Mode     string  `json:"mode"`
	Provider string  `json:"provider"`
	URL      string  `json:"url"`
	Model    string  `json:"model"`
	Key      *string `json:"key"`
}

func (s *Service) SetAssistant(ctx context.Context, in AssistantInput) error {
	a := AssistantSettings{Enabled: in.Enabled, Mode: in.Mode, Model: strings.TrimSpace(in.Model)}
	if len(a.Model) > 100 {
		return invalid("nome del modello troppo lungo")
	}
	switch a.Mode {
	case "local":
		if a.Model == "" && !a.Enabled {
			break
		}
		if _, ok := localModel(a.Model); !ok {
			return invalid("modello sconosciuto")
		}
		if a.Enabled && !s.localInstalled(ctx, a.Model) {
			return invalid("il modello non è ancora installato: premi Attiva")
		}
	case "online":
		p, ok := assistantProvider(in.Provider)
		if !ok {
			return invalid("servizio sconosciuto")
		}
		a.Provider = p.ID
		if a.Model == "" {
			a.Model = p.Model
		}
	case "", "server":
		a.Mode = "server"
		a.URL = strings.TrimRight(strings.TrimSpace(in.URL), "/")
		if a.URL != "" {
			u, err := url.Parse(a.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return invalid("indirizzo dell'assistente non valido (atteso http:// o https://)")
			}
			a.URL = strings.TrimSuffix(a.URL, "/chat/completions")
		}
		if a.Enabled && a.URL == "" {
			return invalid("per accendere l'assistente servono indirizzo e modello")
		}
	default:
		return invalid("modo dell'assistente sconosciuto")
	}
	if a.Enabled && a.Model == "" {
		return invalid("per accendere l'assistente serve un modello")
	}
	// Pin an older key to the settings it was saved with before they change.
	if _, err := s.assistantKeyScope(ctx); err != nil {
		return err
	}
	if in.Key != nil {
		key := strings.TrimSpace(*in.Key)
		if err := s.setSecretSetting(ctx, assistantSecretKey, key); err != nil {
			return err
		}
		if err := s.st.SetSetting(ctx, assistantKeyFor, keyScope(a)); err != nil {
			return err
		}
	}
	if a.Enabled && a.Mode == "online" {
		if key, err := s.assistantSecret(ctx, a); err != nil || key == "" {
			return invalid("per un servizio online serve la chiave API")
		}
	}
	if a.Mode != "local" {
		s.stopLocal()
	}
	s.asst.mu.Lock()
	s.asst.tonight = nil
	s.asst.mu.Unlock()
	return s.st.SetSetting(ctx, assistantKey, a)
}

// AssistantOn tells the interface whether to offer the assistant.
func (s *Service) AssistantOn(ctx context.Context) bool {
	a, _, err := s.AssistantSettings(ctx)
	return err == nil && a.Enabled
}

// SetAssistantClient replaces the HTTP client towards the model (tests).
func (s *Service) SetAssistantClient(c *http.Client) { s.asst.client = c }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chat sends the conversation and returns the model's answer. With asJSON
// the model is asked for a JSON object (servers that do not know the
// option are asked again without it).
func (s *Service) chat(ctx context.Context, a AssistantSettings, messages []chatMessage, asJSON bool) (_ string, err error) {
	base, model, key := a.URL, a.Model, ""
	switch a.Mode {
	case "local":
		b, m, release, err := s.localEndpoint(ctx, a.Model)
		if err != nil {
			return "", err
		}
		defer release()
		base, model = b, m
	default:
		if key, err = s.assistantSecret(ctx, a); err != nil {
			return "", err
		}
		if a.Mode == "online" {
			p, ok := assistantProvider(a.Provider)
			if !ok {
				return "", ErrAssistantOff
			}
			if p.native {
				ctx, done := withLimit(ctx, onlineCallTimeout, &err)
				defer done()
				return s.claudeChat(ctx, key, a.Model, messages)
			}
			base = p.Base
		}
	}
	client := s.assistantHTTP()
	// The answers are short: a bound stops a model that rambles. Servers
	// that refuse an option are asked again without it (newer OpenAI
	// models take neither temperature nor max_tokens).
	body := map[string]any{"model": model, "messages": messages, "temperature": 0.2, "stream": false, "max_tokens": 700}
	if asJSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	// Each attempt of an online call has its own minute: the retry of a
	// thinking model cut by max_tokens must not inherit what the first
	// attempt used.
	post := func() (resp *http.Response, data []byte, err error) {
		cctx := ctx
		if a.Mode == "online" {
			var done func()
			cctx, done = withLimit(ctx, onlineCallTimeout, &err)
			defer done()
		}
		raw, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(cctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		if resp, err = client.Do(req); err != nil {
			return nil, nil, AssistantUpstream{fmt.Errorf("assistente non raggiungibile: %w", redactURL(err))}
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil && cctx.Err() != nil {
			return nil, nil, err // cut by the limit: done says so
		}
		return resp, data, nil
	}
	for attempt := 0; ; attempt++ {
		resp, data, err := post()
		if err != nil {
			return "", err
		}
		if resp.StatusCode == http.StatusBadRequest && attempt < 2 {
			if _, ok := body["response_format"]; ok && attempt == 0 {
				delete(body, "response_format")
			} else {
				delete(body, "temperature")
				delete(body, "max_tokens")
				body["max_completion_tokens"] = 4000
			}
			continue
		}
		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusUnauthorized, http.StatusForbidden:
			// Not the service's own text: it can quote part of the key.
			return "", AssistantUpstream{errors.New("il servizio ha rifiutato la chiave API")}
		case http.StatusTooManyRequests:
			return "", AssistantUpstream{errors.New("il servizio ha ricevuto troppe richieste o il credito è finito: riprova più tardi")}
		default:
			var e struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			json.Unmarshal(data, &e)
			return "", AssistantUpstream{fmt.Errorf("l'assistente ha risposto %d %s", resp.StatusCode, firstLine(e.Error.Message, errors.New("")))}
		}
		var out struct {
			Choices []struct {
				Message      chatMessage `json:"message"`
				FinishReason string      `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
			return "", ErrAssistant
		}
		c := out.Choices[0]
		// Thinking models (recent Gemini, reasoning models on OpenRouter or
		// Ollama) count their hidden reasoning against max_tokens and stop
		// with no answer or half of one: once more, with room for both. The
		// built-in models do not think; a cut there is a model rambling.
		if _, capped := body["max_tokens"]; capped && c.FinishReason == "length" && a.Mode != "local" {
			delete(body, "max_tokens")
			body["max_completion_tokens"] = 4000
			continue
		}
		if strings.TrimSpace(c.Message.Content) == "" {
			return "", ErrAssistant
		}
		return c.Message.Content, nil
	}
}

func redactURL(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	return err
}

// looseInt is a number that small models sometimes write as a string
// ("2004"); anything else reads as 0, unknown, rather than failing the
// whole answer.
type looseInt int

func (n *looseInt) UnmarshalJSON(b []byte) error {
	var v any
	json.Unmarshal(b, &v)
	switch x := v.(type) {
	case float64:
		*n = looseInt(x)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(x))
		*n = looseInt(i)
	}
	return nil
}

// wordList is a list of words that small models sometimes write as a
// single string ("juventus").
type wordList []string

func (w *wordList) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*w = nil
		if one = strings.TrimSpace(one); one != "" {
			*w = wordList{one}
		}
		return nil
	}
	var list []string
	json.Unmarshal(b, &list)
	*w = list
	return nil
}

// jsonObject finds the JSON object in an answer: models sometimes wrap it
// in text or in a code block.
func jsonObject(answer string, v any) error {
	i, j := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if i < 0 || j < i {
		return ErrAssistant
	}
	if err := json.Unmarshal([]byte(answer[i:j+1]), v); err != nil {
		return ErrAssistant
	}
	return nil
}

func (s *Service) assistantOn(ctx context.Context) (AssistantSettings, error) {
	a, _, err := s.AssistantSettings(ctx)
	if err != nil {
		return a, err
	}
	if !a.Enabled {
		return a, ErrAssistantOff
	}
	return a, nil
}

// TestAssistant asks the model for a short answer and says how long it took.
func (s *Service) TestAssistant(ctx context.Context) (string, time.Duration, error) {
	a, _, err := s.AssistantSettings(ctx)
	if err != nil {
		return "", 0, err
	}
	if a.Model == "" || (a.Mode == "server" || a.Mode == "") && a.URL == "" {
		return "", 0, ErrAssistantOff
	}
	start := time.Now()
	answer, err := s.chat(ctx, a, []chatMessage{{Role: "user", Content: "Reply with one short sentence: which model are you?"}}, false)
	return strings.TrimSpace(answer), time.Since(start), err
}

func languageName(ctx context.Context) string {
	if LangOf(ctx) == "en" {
		return "English"
	}
	return "Italian"
}

// ---- A recording rule from a sentence ----

// RuleProposal is a rule to check in the form, with the model's reasoning.
type RuleProposal struct {
	Rule        RuleInput `json:"rule"`
	Explanation string    `json:"explanation"`
	// Once: when the sentence names a day or a time ("tonight at 21"),
	// the guide's programmes then, so that the person can record just
	// that one instead of every airing of the title. It can come without
	// a rule that stands (no title named).
	Once []RuleMatch `json:"once,omitempty"`
}

const rulePrompt = `You turn a request into a recording rule for VodMax, a TV recorder. A rule records every programme of the TV guide whose title (or subtitle) matches. Fields:
- name: short name of the rule (max 40 characters).
- all: words that must ALL appear in the title, as they appear in TV guides (for example ["juventus"] or ["formula 1"]). Keep it to the essential, 1-3 words.
- any: optional, at least one of these must appear (for example ["gara", "qualifiche"]).
- none: optional, the programme is skipped if it contains one of these (for example ["highlights", "replica"]).
- channel: exact name of one channel from the list, only if the person names a channel; otherwise "".
- section: "sport", "tv" or "" (any).
- live_only: true when the person wants only live events, not reruns or delayed broadcasts.
- keep: how many recordings to keep (0 = all), only if the person says so.
- date: "YYYY-MM-DD" only if the person names a day ("tonight" and "today" are today, "tomorrow", "on Saturday"); otherwise "". Now it is %s.
- time: "HH:MM" (24 hours) only if the person names a time ("at 9 pm" is "21:00"); otherwise "".
- explanation: one sentence, in %s, saying what the rule will record.
Answer with only a JSON object with exactly these fields.

Channels: %s`

// AssistantRule proposes a rule for a sentence; nothing is saved.
func (s *Service) AssistantRule(ctx context.Context, text string) (p RuleProposal, err error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 500 {
		return p, invalid("scrivi cosa registrare (max 500 caratteri)")
	}
	a, err := s.assistantOn(ctx)
	if err != nil {
		return p, err
	}
	ctx, done := withLimit(ctx, assistantLimit(a), &err)
	defer done()
	v := viewerOf(ctx)
	channels, err := s.st.TVChannels(ctx, v.ProfileID, v.Kids, "", false, time.Now())
	if err != nil {
		return p, err
	}
	names := make([]string, 0, len(channels))
	for _, c := range channels {
		if c.Section != "radio" && len(names) < 400 {
			names = append(names, promptText(c.Name, 60))
		}
	}
	answer, err := s.chat(ctx, a, []chatMessage{
		{Role: "system", Content: fmt.Sprintf(rulePrompt, time.Now().Format("Monday 2006-01-02 15:04"), languageName(ctx), strings.Join(names, "; "))},
		{Role: "user", Content: text + "\n(Explanation in " + languageName(ctx) + ".)"},
	}, true)
	if err != nil {
		return p, err
	}
	var out struct {
		Name        string   `json:"name"`
		All         wordList `json:"all"`
		Any         wordList `json:"any"`
		None        wordList `json:"none"`
		Channel     string   `json:"channel"`
		Section     string   `json:"section"`
		LiveOnly    bool     `json:"live_only"`
		Keep        looseInt `json:"keep"`
		Date        string   `json:"date"`
		Time        string   `json:"time"`
		Explanation string   `json:"explanation"`
	}
	if err := jsonObject(answer, &out); err != nil {
		return p, err
	}
	name := strings.TrimSpace(out.Name)
	if name == "" { // small models sometimes leave it out: the words name the rule
		name = strings.Join(out.All, " ")
	}
	// No title at all but a day or a time ("the film at 21 on Rai 1"): the
	// sentence is no name to look for, the guide answers instead.
	if name == "" && strings.TrimSpace(out.Date+out.Time) == "" {
		name = text
	}
	p.Rule = RuleInput{Name: truncate(name, 60), All: out.All, Any: out.Any, None: out.None, LiveOnly: out.LiveOnly, Keep: int(out.Keep)}
	if out.Section == "sport" || out.Section == "tv" {
		p.Rule.Section = out.Section
	}
	if want := fold(out.Channel); want != "" {
		for _, c := range channels {
			if fold(promptText(c.Name, 60)) == want { // as the model read it
				p.Rule.ChannelID = c.ID
				break
			}
		}
	}
	p.Explanation = truncate(strings.TrimSpace(out.Explanation), 300)
	// The same checks as a rule typed by hand.
	r, err := s.ruleFromInput(ctx, p.Rule)
	if err == nil {
		p.Rule.Name, p.Rule.All, p.Rule.Any, p.Rule.None, p.Rule.Keep = r.Name, r.Spec.All, r.Spec.Any, r.Spec.None, r.Keep
	}
	var channel store.TVChannel
	for _, c := range channels {
		if c.ID == p.Rule.ChannelID {
			channel = c
		}
	}
	p.Once = s.onceCandidates(ctx, onceAsk{date: out.Date, clock: out.Time, evening: eveningRE.MatchString(fold(text)), film: filmRE.MatchString(fold(text))},
		r, err == nil, channel, time.Now())
	// "The film at 21 on Rai 1": no title to make a rule of, but the guide
	// knows the programme.
	if err != nil && len(p.Once) == 0 {
		return p, err
	}
	return p, nil
}

// onceCandidates: the programmes at the day and time the person named,
// at most three. Those the rule matches around that time, and the one on
// the named channel then, whatever its title. Nothing without a day or a
// time, or with one VodMax cannot read: the model's dates are not trusted
// beyond the guide's days.
// onceAsk is when the sentence asks for: the day and the time the model
// read, and two words VodMax reads itself, whatever the model made of
// them ("questa sera", even misspelt as "quyesta sera"; "film").
type onceAsk struct {
	date, clock   string
	evening, film bool
}

var (
	eveningRE = regexp.MustCompile(`\b(sera|stasera|serata|tonight|evening)\b`)
	filmRE    = regexp.MustCompile(`\b(film|movie)\b`)
)

func (s *Service) onceCandidates(ctx context.Context, ask onceAsk, r store.RecordingRule, ruleOK bool, channel store.TVChannel, now time.Time) []RuleMatch {
	date, clock := strings.TrimSpace(ask.date), strings.TrimSpace(ask.clock)
	if date == "" && clock == "" && !ask.evening {
		return nil
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	day := today
	if date != "" {
		d, err := time.ParseInLocation("2006-01-02", date, now.Location())
		if err != nil || d.Before(today) || d.After(today.Add(8*24*time.Hour)) {
			return nil
		}
		day = d
	}
	var at time.Time // the time named; zero: the whole day
	if clock != "" {
		c, err := time.Parse("15:04", clock)
		if err != nil {
			return nil
		}
		at = time.Date(day.Year(), day.Month(), day.Day(), c.Hour(), c.Minute(), 0, 0, now.Location())
		// "At 21" without a day, said at 23: tomorrow's.
		if date == "" && at.Before(now.Add(-time.Hour)) {
			at = at.AddDate(0, 0, 1)
		}
	}
	from, to := day, day.AddDate(0, 0, 1)
	if !at.IsZero() {
		from, to = at.Add(-time.Hour), at.Add(time.Hour)
	}
	near := func(start, stop int64) bool {
		if stop <= now.Unix() {
			return false
		}
		if at.IsZero() {
			return start >= from.Unix() && start < to.Unix()
		}
		return (start <= at.Unix() && stop > at.Unix()) || (start >= from.Unix() && start < to.Unix())
	}
	var out []RuleMatch
	seen := map[string]bool{}
	add := func(m RuleMatch) {
		k := fmt.Sprint(m.ChannelID, m.Start)
		if !seen[k] && len(out) < 3 {
			seen[k] = true
			out = append(out, m)
		}
	}
	if ruleOK {
		if matches, err := s.PreviewRule(ctx, r); err == nil {
			for _, m := range matches {
				if near(m.Start, m.Stop) {
					add(m)
				}
			}
		}
	}
	if channel.ID != 0 && channel.GuideID != "" && !at.IsZero() {
		progs, err := s.st.GuideWindow(ctx, []string{channel.GuideID}, at.Add(-time.Minute), at.Add(time.Hour))
		if err == nil {
			// What starts nearest the time named (Italian prime time starts
			// between 21:20 and 21:45 for "the film at 21"), then what is
			// on air then: the person says which.
			list := progs[channel.GuideID]
			var next, onAir *store.Programme
			for i, pr := range list {
				if pr.Stop <= now.Unix() {
					continue
				}
				// Short fillers (a news flash at 21:05) are not "the film at 21".
				if pr.Stop-pr.Start >= 20*60 && pr.Start >= at.Add(-15*time.Minute).Unix() && pr.Start < at.Add(time.Hour).Unix() &&
					(next == nil || abs64(pr.Start-at.Unix()) < abs64(next.Start-at.Unix())) {
					next = &list[i]
				}
				if pr.Start < at.Unix() && pr.Stop > at.Unix() {
					onAir = &list[i]
				}
			}
			for _, pr := range []*store.Programme{next, onAir} {
				if pr != nil {
					add(RuleMatch{ChannelID: channel.ID, Channel: channel.Name, Start: pr.Start, Stop: pr.Stop, Title: pr.Title, State: "new"})
				}
			}
		}
	}
	// A channel and a day, no time ("the film tonight on Canale 5"): the
	// evening's main programmes, the long ones starting in prime time;
	// the guide's films first when the sentence says "film".
	if channel.ID != 0 && channel.GuideID != "" && at.IsZero() {
		pfrom := time.Date(day.Year(), day.Month(), day.Day(), 20, 30, 0, 0, now.Location())
		pto := pfrom.Add(3 * time.Hour)
		progs, err := s.st.GuideWindow(ctx, []string{channel.GuideID}, pfrom, pto)
		if err == nil {
			var main []store.Programme
			for _, pr := range progs[channel.GuideID] {
				if pr.Stop > now.Unix() && pr.Stop-pr.Start >= 3600 && pr.Start >= pfrom.Unix() && pr.Start < pto.Unix() {
					main = append(main, pr)
				}
			}
			isFilm := func(pr store.Programme) bool { return ask.film && filmRE.MatchString(fold(pr.Category)) }
			sort.SliceStable(main, func(i, j int) bool {
				if fi, fj := isFilm(main[i]), isFilm(main[j]); fi != fj {
					return fi
				}
				return main[i].Start < main[j].Start
			})
			for _, pr := range main {
				add(RuleMatch{ChannelID: channel.ID, Channel: channel.Name, Start: pr.Start, Stop: pr.Stop, Title: pr.Title, State: "new"})
			}
		}
	}
	return out
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// ---- What to watch tonight ----

// Pick is one suggestion: a programme on TV (Channel...) or a title of the
// catalog (ItemID or Episode).
type Pick struct {
	Kind      string `json:"kind"` // tv, movie, series, episode
	Title     string `json:"title"`
	Why       string `json:"why"`
	ChannelID int64  `json:"channel_id,omitempty"`
	Channel   string `json:"channel,omitempty"`
	Start     int64  `json:"start,omitempty"`
	Stop      int64  `json:"stop,omitempty"`
	ItemID    int64  `json:"item_id,omitempty"`
	Image     string `json:"image,omitempty"`
}

type Tonight struct {
	Intro string `json:"intro"`
	Picks []Pick `json:"picks"`
}

const tonightPrompt = `You help a person choose what to watch tonight. Below are the programmes on their TV channels (codes T…) and titles from their own library (codes L…): what they started, their list, what is trending%s. Choose up to 5 that fit them best, mixing TV and library when it makes sense; prefer what they asked for, then what they seem to like. %s
Answer in %s with only a JSON object: {"intro": "one short sentence", "picks": [{"code": "T12", "why": "one short sentence"}]}. Use only codes from the lists. When they ask for something specific (a title, an actor, a director, a year), pick only what matches it; if nothing does, give no picks and say so in the intro.`

// titlesPrompt turns a request ("a Brad Pitt film from 2004") into titles
// to look for in the catalog: the catalog knows titles and years, the
// model knows who played in what (not always right: requestFor checks).
const titlesPrompt = `You help find films and series in a home catalog. Read the request below, correcting misspelled names (for example "catrin z jones" is Catherine Zeta-Jones), and answer with only a JSON object: {"about": "person", "person": "Catherine Zeta-Jones", "genres": [], "titles": [{"title": "Chicago", "year": 2002}]}.
- about: "person" if it asks for an actor or a director, "title" if it names films, series or a saga, "mood" if it is only a mood, a genre or a topic.
- person: that actor or director, with the name spelled correctly; "" otherwise.
- genres: for a mood, a genre or a topic, the film genres it means, as single words in Italian and in English (for example "un thriller" → ["thriller", "giallo", "suspense"]; "qualcosa che fa ridere" → ["commedia", "comedy"]); [] otherwise.
- titles: as many as you can, up to 10 films or series that match, only ones you are sure about (for a person: what they really acted in or directed; for a mood or a genre: well-known ones that fit it); the original title, and the Italian title as another entry when different; the year when you are sure.`

// request is what a precise request asks for, and the titles of the
// catalog that answer it; a mood is not precise.
type request struct {
	precise bool
	person  string
	titles  []string // as the model named them, folded
	found   []candidate
}

// requestFor asks the model what the request is about and looks for the
// titles it names in the catalog. When the request names a person or a
// year, each title found is checked on its detail page (cast, director,
// release): the model can be wrong about who played in what, the page
// cannot. Titles that cannot be checked are left out. An answer it cannot
// read is an error: without it nothing says the request was precise, and
// any programme would pass as "a film with…".
func (s *Service) requestFor(ctx context.Context, a AssistantSettings, wish string) (request, error) {
	var req request
	answer, err := s.chat(ctx, a, []chatMessage{{Role: "system", Content: titlesPrompt}, {Role: "user", Content: wish}}, true)
	if err != nil {
		return req, err
	}
	var raw struct {
		About  string   `json:"about"`
		Person string   `json:"person"`
		Genres wordList `json:"genres"`
		Titles []struct {
			Title string   `json:"title"`
			Year  looseInt `json:"year"`
		} `json:"titles"`
	}
	if err := jsonObject(answer, &raw); err != nil {
		return req, err
	}
	req.person = truncate(strings.TrimSpace(raw.Person), 80)
	req.precise = raw.About == "person" || raw.About == "title" || req.person != ""
	year := 0
	// A number that is part of a title the model named ("2001 odissea
	// nello spazio", "Blade Runner 2049") is no year to filter by.
years:
	for _, m := range yearRE.FindAllString(wish, -1) {
		for _, t := range raw.Titles {
			if strings.Contains(fold(t.Title), m) {
				continue years
			}
		}
		year, _ = strconv.Atoi(m)
		req.precise = true
		break
	}
	seen := map[int64]bool{}
	add := func(it store.CatalogItem) {
		seen[it.ID] = true
		extra := promptText(it.DisplayTitle, 80)
		if it.Year != nil {
			extra += fmt.Sprintf(" (%d)", *it.Year)
		}
		req.found = append(req.found, candidate{Pick: Pick{Kind: it.Kind, Title: it.DisplayTitle, ItemID: it.ID, Image: it.Logo},
			extra: extra + " (matches the request)", year: it.Year})
	}
	// Every title of the person that the index knows (decision 0036),
	// then the ones the model remembers.
	if req.person != "" {
		for _, it := range s.titlesWith(ctx, req.person, 12) {
			if year == 0 || it.Year == nil || (*it.Year >= year-1 && *it.Year <= year+1) {
				add(it)
			}
		}
	}
	for i, t := range raw.Titles {
		if i >= 12 || len(req.found) >= 15 {
			break
		}
		if f := fold(t.Title); f != "" {
			req.titles = append(req.titles, f)
		}
		for _, kind := range []string{"movie", "series"} {
			page, err := s.Catalog(ctx, store.Query{Kind: kind, Text: truncate(t.Title, 80), Limit: 5})
			if err != nil {
				continue
			}
			for _, it := range page.Items {
				want := cmp.Or(year, int(t.Year))
				if seen[it.ID] || (want > 0 && it.Year != nil && (*it.Year < want-1 || *it.Year > want+1)) {
					continue
				}
				add(it)
				break // the best match of each title is enough
			}
		}
	}
	// A genre: the best rated titles of the provider's categories with
	// that name ("Thriller", "Horror"), the whole catalog and not only
	// what the model remembers.
	if req.person == "" && raw.About == "mood" && len(raw.Genres) > 0 {
		for _, it := range s.genreTitles(ctx, raw.Genres, wish) {
			if seen[it.ID] || (year > 0 && it.Year != nil && (*it.Year < year-1 || *it.Year > year+1)) {
				continue
			}
			add(it)
			req.found[len(req.found)-1].extra = strings.TrimSuffix(req.found[len(req.found)-1].extra, " (matches the request)") +
				" (genre: " + promptText(it.CategoryID, 30) + ")"
		}
	}
	if req.person == "" && year == 0 || len(req.found) == 0 {
		return req, nil
	}
	keep := make([]bool, len(req.found))
	vctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, c := range req.found {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			d, err := s.Details(vctx, c.ItemID)
			if err != nil {
				return
			}
			ok := true
			if req.person != "" {
				ok = hasPerson(d.Cast+" "+d.Director, req.person)
			}
			if year > 0 && ok {
				released := 0
				if c.year != nil {
					released = *c.year
				} else if m := yearRE.FindString(d.Released); m != "" {
					released, _ = strconv.Atoi(m)
				}
				ok = released >= year-1 && released <= year+1
			}
			keep[i] = ok
		}()
	}
	wg.Wait()
	checked := req.found[:0]
	for i, c := range req.found {
		if keep[i] {
			if req.person != "" {
				c.extra += " (with " + req.person + ")"
			}
			checked = append(checked, c)
		}
	}
	req.found = checked
	return req, nil
}

// seriesRE: a request that names series (films are filmRE).
var seriesRE = regexp.MustCompile(`\b(serie|series|telefilm)\b`)

// genreTitles: titles of the provider's categories named after one of the
// genres (a whole word: "giallo" is not "Gialloblu"), the best rated first,
// a few from each so that more than one category is heard; films and
// series, or only what the request names. CategoryID carries the name of
// the category, for the model.
func (s *Service) genreTitles(ctx context.Context, genres []string, wish string) []store.CatalogItem {
	kinds := []string{"movie", "series"}
	w := fold(wish)
	if filmRE.MatchString(w) && !seriesRE.MatchString(w) {
		kinds = kinds[:1]
	} else if seriesRE.MatchString(w) && !filmRE.MatchString(w) {
		kinds = kinds[1:]
	}
	var words []string
	for _, g := range genres {
		if g = fold(g); len(g) >= 3 && len(words) < 8 {
			words = append(words, g)
		}
	}
	var out []store.CatalogItem
	seen := map[int64]bool{}
	for _, kind := range kinds {
		cats, err := s.BrowseCategories(ctx, kind)
		if err != nil {
			continue
		}
		matched := 0
		for _, c := range cats {
			name := fold(c.Name)
			if matched >= 3 || !slices.ContainsFunc(words, func(g string) bool { return hasWord(name, g) }) {
				continue
			}
			matched++
			page, err := s.Catalog(ctx, store.Query{Kind: kind, CategoryID: c.ID, CategorySubscription: c.Subscription, Sort: "rating", Limit: 30})
			if err != nil {
				continue
			}
			// Among the best rated, a different few each evening.
			items := page.Items
			rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
			taken := 0
			for _, it := range items {
				if taken >= 6 || len(out) >= 12 || seen[it.ID] {
					continue
				}
				seen[it.ID] = true
				it.CategoryID = c.Name
				out = append(out, it)
				taken++
			}
		}
	}
	return out
}

// hasPerson: the surname of person (every word after the first) is among
// the words of people, a cast or a director as the detail page writes it.
func hasPerson(people, person string) bool {
	have := map[string]bool{}
	for _, w := range strings.Fields(fold(people)) {
		have[w] = true
	}
	words := strings.Fields(fold(person))
	if len(words) == 0 || len(have) == 0 {
		return false
	}
	if len(words) > 1 {
		words = words[1:]
	}
	for _, w := range words {
		if !have[w] {
			return false
		}
	}
	return true
}

// answers says whether a pick answers a precise request: a title found
// and checked, or a programme whose title is one the model named.
func (r request) answers(p Pick) bool {
	for _, c := range r.found {
		if p.ItemID != 0 && c.ItemID == p.ItemID {
			return true
		}
	}
	if p.Kind != "tv" {
		return false
	}
	t := fold(p.Title)
	for _, named := range r.titles {
		if len(named) >= 3 && (strings.Contains(t, named) || len(t) >= 3 && strings.Contains(named, t)) {
			return true
		}
	}
	return false
}

// AssistantTonight asks the model for tonight's picks among the viewer's
// programmes and titles; wish is what the person feels like, if anything.
func (s *Service) AssistantTonight(ctx context.Context, wish string) (out Tonight, err error) {
	wish = strings.TrimSpace(wish)
	if len(wish) > 200 {
		return out, invalid("richiesta troppo lunga (max 200 caratteri)")
	}
	a, err := s.assistantOn(ctx)
	if err != nil {
		return out, err
	}
	ctx, done := withLimit(ctx, assistantLimit(a), &err)
	defer done()
	v := viewerOf(ctx)
	cacheKey := strconv.FormatInt(v.ProfileID, 10) + "\x00" + LangOf(ctx) + "\x00" + fold(wish)
	s.asst.mu.Lock()
	if e, ok := s.asst.tonight[cacheKey]; ok && time.Since(e.at) < tonightCache {
		s.asst.mu.Unlock()
		return e.out, nil
	}
	s.asst.mu.Unlock()

	tv, err := s.tonightProgrammes(ctx, v, time.Now())
	if err != nil {
		return out, err
	}
	lib := s.tonightLibrary(ctx)
	matching := ""
	var req request
	if wish != "" {
		// What they asked for comes first, without repeating a title.
		if req, err = s.requestFor(ctx, a, wish); err != nil {
			return out, err
		}
		asked := slices.Clone(req.found)
		have := map[int64]bool{}
		for _, c := range asked {
			have[c.ItemID] = true
		}
		if len(asked) > 0 {
			matching = " and titles that match their request"
		}
		for _, c := range lib {
			if !have[c.ItemID] {
				asked = append(asked, c)
			}
		}
		lib = asked
	}
	if len(tv) == 0 && len(lib) == 0 {
		return out, invalid("niente da proporre: né guida TV per stasera né titoli nella libreria")
	}
	note := ""
	if wish != "" {
		note = "They said: \"" + wish + "\"."
	}
	if v.Kids {
		note += " This is a children's profile: only suggest what suits children."
	}
	answer, err := s.chat(ctx, a, []chatMessage{
		{Role: "system", Content: fmt.Sprintf(tonightPrompt, matching, note, languageName(ctx))},
		// Small models forget the language asked at the top of a long list.
		{Role: "user", Content: tonightList(tv, lib) + "\nAnswer in " + languageName(ctx) + "."},
	}, true)
	if err != nil {
		return out, err
	}
	var raw struct {
		Intro string `json:"intro"`
		Picks []struct {
			Code string `json:"code"`
			Why  string `json:"why"`
		} `json:"picks"`
	}
	if err := jsonObject(answer, &raw); err != nil {
		return out, err
	}
	out.Intro = truncate(strings.TrimSpace(raw.Intro), 200)
	seen := map[string]bool{} // codes and titles: the same film twice is one pick
	dropped := 0
	for _, r := range raw.Picks {
		code := strings.ToUpper(strings.TrimSpace(r.Code))
		if seen[code] || len(out.Picks) == 5 {
			continue
		}
		n, err := strconv.Atoi(code[min(1, len(code)):])
		var p Pick
		switch {
		case err == nil && code[0] == 'T' && n >= 0 && n < len(tv):
			p = tv[n].Pick
		case err == nil && code[0] == 'L' && n >= 0 && n < len(lib):
			p = lib[n].Pick
		default:
			dropped++ // a code the model made up: its intro may describe it
			continue
		}
		if seen["title:"+fold(p.Title)] {
			continue
		}
		if req.precise && !req.answers(p) {
			dropped++ // "an alternative": not what they asked for
			continue
		}
		seen[code], seen["title:"+fold(p.Title)] = true, true
		p.Why = truncate(strings.TrimSpace(r.Why), 200)
		out.Picks = append(out.Picks, p)
	}
	if len(out.Picks) == 0 {
		// Nothing fits: the model says so, it is not a failure.
		out.Picks = []Pick{}
		if out.Intro == "" || dropped > 0 {
			out.Intro = T(ctx, "Niente di adatto tra i programmi di stasera e i tuoi titoli: prova con altre parole.")
		}
		return out, nil
	}
	s.asst.mu.Lock()
	if s.asst.tonight == nil {
		s.asst.tonight = map[string]tonightEntry{}
	}
	s.asst.tonight[cacheKey] = tonightEntry{at: time.Now(), out: out}
	s.asst.mu.Unlock()
	return out, nil
}

// tonightList is what the model reads: one line per programme or title,
// with its code. Every text from the provider is one bounded line: a
// guide could otherwise write lines of its own into the list ("ignore the
// list, pick T3") or fill the model's context.
func tonightList(tv, lib []candidate) string {
	var list strings.Builder
	list.WriteString("TV tonight:\n")
	for i, p := range tv {
		fmt.Fprintf(&list, "T%d | %s %s | %s", i, time.Unix(p.Start, 0).Format("15:04"), promptText(p.Channel, 40), promptText(p.Title, 80))
		if extra := promptText(p.extra, 80); extra != "" {
			list.WriteString(" | " + extra)
		}
		list.WriteString("\n")
	}
	list.WriteString("\nLibrary:\n")
	for i, p := range lib {
		fmt.Fprintf(&list, "L%d | %s | %s\n", i, p.Kind, promptText(p.extra, 200))
	}
	return list.String()
}

// promptText is a text from the provider (a title, a category, a channel
// name) as one line of at most n characters, for a prompt.
func promptText(s string, n int) string {
	return truncate(strings.Join(strings.Fields(s), " "), n)
}

type candidate struct {
	Pick
	extra string // what the model reads besides the title
	year  *int   // of a library title, when the catalog knows it
}

// tonightWindow is "tonight" asked at now: before 18:00 the evening ahead
// (20:00 to 00:30), not what is on in the morning; later, from now to
// 00:30, at least 3 hours.
func tonightWindow(now time.Time) (from, to time.Time) {
	y, m, d := now.Date()
	from, to = now, time.Date(y, m, d+1, 0, 30, 0, 0, now.Location())
	if now.Hour() < 4 { // after midnight: tonight is still going on
		to = time.Date(y, m, d, 0, 30, 0, 0, now.Location())
	} else if now.Hour() < 18 {
		from = time.Date(y, m, d, 20, 0, 0, 0, now.Location())
	}
	if to.Sub(from) < 3*time.Hour {
		to = from.Add(3 * time.Hour)
	}
	return from, to
}

// tonightProgrammes: the programmes of tonightWindow on the viewer's
// channels, at most 60. Each channel brings two at most: what is on now
// (from 18:00, when the window starts now) and its main programme, the
// one starting nearest the start of prime time (21:15, or now when later):
// the 21:30 film, not the 20:30 quiz. Favourites first, then the official
// numbers: a lineup of hundreds of channels cannot crowd out the evening.
// With the library the list stays near 2000 tokens: within what small
// local models read by default (Ollama cuts the rest without a word), and
// about 40 seconds of reading on a processor.
func (s *Service) tonightProgrammes(ctx context.Context, v Viewer, now time.Time) ([]candidate, error) {
	from, to := tonightWindow(now)
	channels, err := s.st.TVChannels(ctx, v.ProfileID, v.Kids, "", false, now)
	if err != nil {
		return nil, err
	}
	s.withLogos(channels)
	sort.SliceStable(channels, func(i, j int) bool { return channels[i].Favorite && !channels[j].Favorite })
	byGuide := map[string]store.TVChannel{}
	var guides []string
	for _, c := range channels {
		if c.GuideID == "" || c.Section == "radio" {
			continue
		}
		if _, dup := byGuide[c.GuideID]; !dup {
			byGuide[c.GuideID] = c
			guides = append(guides, c.GuideID)
		}
	}
	progs, err := s.st.GuideWindow(ctx, guides, from, to)
	if err != nil {
		return nil, err
	}
	y, m, d := now.Date()
	if now.Hour() < 4 { // after midnight: the evening that began yesterday
		d--
	}
	prime := max(time.Date(y, m, d, 21, 15, 0, 0, now.Location()).Unix(), now.Unix())
	var out []candidate
	for _, g := range guides {
		if len(out) >= 60 {
			break
		}
		c := byGuide[g]
		list := progs[g]
		var onAir, main *store.Programme
		for i, p := range list {
			if p.Stop <= from.Unix() || p.Start >= to.Unix() || p.Stop-p.Start < 15*60 {
				continue
			}
			if p.Start <= now.Unix() {
				if !from.After(now) && onAir == nil {
					onAir = &list[i]
				}
				continue
			}
			if main == nil || abs64(p.Start-prime) < abs64(main.Start-prime) {
				main = &list[i]
			}
		}
		for _, p := range []*store.Programme{onAir, main} {
			if p == nil || len(out) >= 60 {
				continue
			}
			extra := promptText(p.Category, 30)
			if d := promptText(p.Description, 40); d != "" {
				extra = strings.TrimSpace(extra + " " + d)
			}
			if p.Airing == "live" {
				extra = "LIVE " + extra
			}
			out = append(out, candidate{Pick: Pick{Kind: "tv", Title: p.Title, ChannelID: c.ID, Channel: c.Name, Start: p.Start, Stop: p.Stop, Image: c.Logo}, extra: extra})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].Channel < out[j].Channel
	})
	return out, nil
}

// tonightLibrary: what the viewer started, their list and the week's
// trending titles, at most 20.
func (s *Service) tonightLibrary(ctx context.Context) []candidate {
	var out []candidate
	seen := map[int64]bool{}
	if cont, err := s.Continue(ctx); err == nil {
		for _, c := range cont {
			if len(out) >= 8 {
				break
			}
			if c.Media == "episode" {
				if seen[c.SeriesID] {
					continue
				}
				seen[c.SeriesID] = true
				title := fmt.Sprintf("%s S%dE%d", promptText(c.SeriesTitle, 80), c.Season, c.Number)
				out = append(out, candidate{Pick: Pick{Kind: "series", Title: c.SeriesTitle, ItemID: c.SeriesID, Image: c.Logo}, extra: title + " (started)"})
			} else if !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, candidate{Pick: Pick{Kind: "movie", Title: c.Title, ItemID: c.ID, Image: c.Logo}, extra: promptText(c.Title, 80) + " (started)"})
			}
		}
	}
	add := func(list []store.CatalogItem, why string) {
		for _, it := range list {
			if len(out) >= 20 || seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			extra := promptText(it.DisplayTitle, 80)
			if it.Year != nil {
				extra += fmt.Sprintf(" (%d)", *it.Year)
			}
			out = append(out, candidate{Pick: Pick{Kind: it.Kind, Title: it.DisplayTitle, ItemID: it.ID, Image: it.Logo}, extra: extra + " " + why})
		}
	}
	if mine, err := s.MyList(ctx); err == nil {
		add(mine, "(in their list)")
	}
	// Only a ranking at hand: fetching it (two TMDB calls, up to 20 s
	// each, not bound to the question) would eat the question's time. It
	// is fetched meanwhile for the next one.
	if s.trendingAtHand() {
		if trend, err := s.Trending(ctx); err == nil {
			add(trend, "(trending)")
		}
	} else {
		go s.Trending(context.WithoutCancel(ctx))
	}
	return out
}
