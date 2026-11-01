package service

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/silirim/vodmax/internal/store"
)

// Recording rules record guide programmes automatically ("every Juventus
// match", "F1, race and qualifying"). They run after every guide refresh
// and every rulesEvery, over the programmes of the next guideFuture.

const (
	rulesEvery   = 10 * time.Minute
	maxRuleWords = 20
)

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// fold lowers case and removes accents and punctuation.
func fold(s string) string {
	s = strings.ToLower(norm.NFKD.String(s))
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(nonWord.ReplaceAllString(s, " ")), " ")
}

// hasWord: a single word matches the same word of text, or one with up to
// two more letters (plurals: "film", "films"); not any word that starts
// with it ("inter" is not "internazionali"). A phrase matches as written,
// on word boundaries.
func hasWord(text, word string) bool {
	w := fold(word)
	if w == "" {
		return false
	}
	if strings.Contains(w, " ") {
		return strings.Contains(" "+text+" ", " "+w+" ")
	}
	for _, t := range strings.Fields(text) {
		if strings.HasPrefix(t, w) && len(t)-len(w) <= 2 {
			return true
		}
	}
	return false
}

// wordAliases are names the guides write in more than one way.
var wordAliases = map[string][]string{
	"formula 1": {"f1"}, "formula uno": {"f1", "formula 1"}, "f1": {"formula 1"},
	"motogp": {"moto gp"}, "moto gp": {"motogp"},
	"juve": {"juventus"},
}

// hasWordOrAlias is hasWord, also trying the other names of word.
func hasWordOrAlias(text, word string) bool {
	if hasWord(text, word) {
		return true
	}
	for _, a := range wordAliases[fold(word)] {
		if hasWord(text, a) {
			return true
		}
	}
	return false
}

// ruleMatches tells whether p, on the channel of group key group, is what
// spec asks for. On the rule's own channel its name counts as part of every
// title: "F1" on Sky Sport F1 is also "GP Messico: Qualifiche".
func ruleMatches(spec store.RuleSpec, p store.Programme, group string) bool {
	text := fold(p.Title + " " + p.Subtitle)
	if spec.Channel != "" && group == spec.Channel {
		text += " " + fold(group)
	}
	for _, w := range spec.All {
		if !hasWordOrAlias(text, w) {
			return false
		}
	}
	if len(spec.Any) > 0 {
		ok := false
		for _, w := range spec.Any {
			if hasWordOrAlias(text, w) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, w := range spec.None {
		if hasWordOrAlias(text, w) {
			return false
		}
	}
	return true
}

func cleanWords(list []string) []string {
	out := []string{}
	for _, w := range list {
		if w = strings.TrimSpace(w); w != "" && fold(w) != "" {
			out = append(out, w)
		}
	}
	return out
}

func (s *Service) checkRule(r *store.RecordingRule) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Spec.All, r.Spec.Any, r.Spec.None = cleanWords(r.Spec.All), cleanWords(r.Spec.Any), cleanWords(r.Spec.None)
	if len(r.Spec.All) == 0 && fold(r.Name) != "" { // "Formula 1": the name is what to look for
		r.Spec.All = []string{r.Name}
	}
	switch {
	case r.Name == "" || len(r.Name) > 60:
		return invalid("nome della regola obbligatorio (max 60 caratteri)")
	case len(r.Spec.All) == 0:
		return invalid("serve almeno una parola che il programma deve contenere")
	case len(r.Spec.All)+len(r.Spec.Any)+len(r.Spec.None) > maxRuleWords:
		return invalid("troppe parole (max %d)", maxRuleWords)
	case r.Keep < 0 || r.Keep > 1000:
		return invalid("numero di registrazioni da tenere non valido")
	}
	switch r.Spec.Section {
	case "", "tv", "sport", "radio":
	default:
		return invalid("sezione non valida")
	}
	return nil
}

// RuleMatch is a programme a rule would record.
type RuleMatch struct {
	ChannelID int64   `json:"channel_id"`
	Channel   string  `json:"channel"`
	Start     int64   `json:"start"`
	Stop      int64   `json:"stop"`
	Title     string  `json:"title"`
	State     string  `json:"state"` // new, scheduled, cancelled, conflict, too_long, rerun
	Note      string  `json:"note,omitempty"`
	Airing    *Airing `json:"airing,omitempty"` // for live-only rules

	group string
	sub   int64
}

// ruleCandidates finds the programmes of the next guideFuture a rule
// matches: one channel per event (the same match on several channels is
// recorded once), ending at least five minutes from now. For a live-only
// rule each one says whether it looks live; reruns are in state "rerun".
func (s *Service) ruleCandidates(ctx context.Context, spec store.RuleSpec) ([]RuleMatch, error) {
	now := time.Now()
	channels, err := s.st.TVChannels(ctx, DefaultProfile, false, spec.Section, false, now)
	if err != nil {
		return nil, err
	}
	var guides []string
	for _, c := range channels {
		if c.GuideID != "" && (spec.Channel == "" || c.GroupKey == spec.Channel) {
			guides = append(guides, c.GuideID)
		}
	}
	from := now.Add(5 * time.Minute)
	if spec.LiveOnly { // the week before tells reruns
		from = now.Add(-store.GuideHistory)
	}
	progs, err := s.st.GuideWindow(ctx, guides, from, now.Add(guideFuture))
	if err != nil {
		return nil, err
	}
	aired := airingIndex{}
	if spec.LiveOnly {
		for _, c := range channels {
			for _, p := range progs[c.GuideID] {
				if ruleMatches(spec, p, c.GroupKey) {
					aired.add(p, c.Name)
				}
			}
		}
	}
	soon := now.Add(5 * time.Minute).Unix()
	seen := map[string]bool{}
	var out []RuleMatch
	for _, c := range channels { // remote-control order: official channels first
		if c.GuideID == "" || (spec.Channel != "" && c.GroupKey != spec.Channel) {
			continue
		}
		it, err := s.st.PlaybackItem(ctx, c.ID)
		if err != nil {
			continue
		}
		for _, p := range progs[c.GuideID] {
			if p.Stop <= soon || !ruleMatches(spec, p, c.GroupKey) {
				continue
			}
			event := fold(p.Title) + "|" + fmt.Sprint(p.Start/600)
			if seen[event] {
				continue
			}
			seen[event] = true
			m := RuleMatch{ChannelID: c.ID, Channel: c.Name, Start: p.Start, Stop: p.Stop, Title: p.Title,
				group: c.GroupKey, sub: it.SubscriptionID}
			if spec.LiveOnly {
				a := airingOf(ctx, p, c.Name, aired)
				m.Airing = &a
				if a.Kind == "rerun" {
					m.State, m.Note = "rerun", a.Why
				}
			}
			out = append(out, m)
		}
	}
	slices.SortStableFunc(out, func(a, b RuleMatch) int { return cmp.Compare(a.Start, b.Start) })
	return out, nil
}

// assess says what would happen to a candidate.
func (s *Service) assess(ctx context.Context, m *RuleMatch) error {
	start, stop := time.Unix(m.Start, 0).Add(-recordBefore).Unix(), time.Unix(m.Stop, 0).Add(recordAfter).Unix()
	switch status, err := s.st.RecordingCovers(ctx, m.group, m.Start+60, m.Stop-60); {
	case err != nil:
		return err
	case status == "cancelled":
		m.State, m.Note = "cancelled", T(ctx, "annullata a mano")
		return nil
	case status != "":
		m.State = "scheduled"
		return nil
	}
	if time.Duration(stop-start)*time.Second > maxRecording {
		m.State, m.Note = "too_long", T(ctx, "più lungo di 6 ore")
		return nil
	}
	why, err := s.recordingConflict(ctx, m.sub, m.group, start, stop)
	if err != nil {
		return err
	}
	if why != "" {
		m.State, m.Note = "conflict", why
		return nil
	}
	m.State = "new"
	return nil
}

// PreviewRule lists what a rule would record now.
func (s *Service) PreviewRule(ctx context.Context, r store.RecordingRule) ([]RuleMatch, error) {
	if err := s.checkRule(&r); err != nil {
		return nil, err
	}
	list, err := s.ruleCandidates(ctx, r.Spec)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].State == "rerun" {
			continue
		}
		if err := s.assess(ctx, &list[i]); err != nil {
			return nil, err
		}
	}
	if list == nil {
		list = []RuleMatch{}
	}
	return list, nil
}

// applyRules schedules what the enabled rules match.
func (s *Service) applyRules(ctx context.Context, log *slog.Logger) {
	rules, err := s.st.RecordingRules(ctx)
	if err != nil {
		return
	}
	for _, r := range rules {
		if !r.Enabled || ctx.Err() != nil {
			continue
		}
		note, err := s.applyRule(ctx, r)
		if err != nil {
			note = T(ctx, "errore: %s", err.Error())
			log.Warn("regola di registrazione", "regola", r.Name, "err", err)
		}
		s.st.SetRecordingRuleResult(ctx, r.ID, note)
	}
	s.mu.Lock()
	s.maint.rulesRun = time.Now()
	s.mu.Unlock()
}

func (s *Service) applyRule(ctx context.Context, r store.RecordingRule) (string, error) {
	list, err := s.ruleCandidates(ctx, r.Spec)
	if err != nil {
		return "", err
	}
	added, already, cancelled, reruns := 0, 0, 0, 0
	var conflicts []string
	for i := range list {
		m := &list[i]
		if m.State == "rerun" {
			reruns++
			continue
		}
		if err := s.assess(ctx, m); err != nil {
			return "", err
		}
		switch m.State {
		case "scheduled":
			already++
		case "cancelled":
			cancelled++
		case "conflict":
			conflicts = append(conflicts, fmt.Sprintf("“%s” %s", m.Title, time.Unix(m.Start, 0).Format("02/01 15:04")))
		case "new":
			it, err := s.st.PlaybackItem(ctx, m.ChannelID)
			if err != nil {
				continue
			}
			rec := store.Recording{ItemID: it.ID, GroupKey: m.group, Channel: m.Channel, Title: m.Title, RuleID: r.ID,
				Start: time.Unix(m.Start, 0).Add(-recordBefore).Unix(), Stop: time.Unix(m.Stop, 0).Add(recordAfter).Unix()}
			if _, err := s.st.AddRecording(ctx, rec); err != nil {
				return "", err
			}
			added++
		}
	}
	if err := s.dropStale(ctx, r, list); err != nil {
		return "", err
	}
	note := T(ctx, "%d in programma", added+already)
	if added > 0 {
		note += T(ctx, " (%d nuove)", added)
	}
	if cancelled > 0 {
		note += T(ctx, ", %d annullate a mano", cancelled)
	}
	if reruns > 0 {
		note += T(ctx, ", %d repliche saltate", reruns)
	}
	if len(list) == 0 {
		note = T(ctx, "nessun programma corrispondente nella guida dei prossimi giorni")
	}
	if len(conflicts) > 0 {
		note += T(ctx, "; non registrabili per mancanza di connessioni: %s", strings.Join(conflicts, ", "))
	}
	return note, nil
}

// dropStale removes the rule's recordings that have not started and that
// no programme of the guide asks for any more (moved, or the rule was
// changed). Only on channels whose guide has programmes for the rule now:
// a guide that failed to load removes nothing.
func (s *Service) dropStale(ctx context.Context, r store.RecordingRule, list []RuleMatch) error {
	recs, err := s.st.RecordingsInState(ctx, "scheduled")
	if err != nil {
		return err
	}
	guided := map[string]bool{}
	for _, m := range list {
		guided[m.group] = true
	}
	later := time.Now().Add(10 * time.Minute).Unix()
	for _, rec := range recs {
		if rec.RuleID != r.ID || rec.Start <= later {
			continue
		}
		if r.Spec.Channel != "" && rec.GroupKey != r.Spec.Channel {
			// The rule was moved to another channel.
			if err := s.st.DeleteRecording(ctx, rec.ID); err != nil {
				return err
			}
			continue
		}
		if !guided[rec.GroupKey] {
			continue
		}
		wanted := slices.ContainsFunc(list, func(m RuleMatch) bool {
			return m.group == rec.GroupKey && m.Start+60 >= rec.Start && m.Stop-60 <= rec.Stop
		})
		if !wanted {
			if err := s.st.DeleteRecording(ctx, rec.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- Rule management ----

func (s *Service) RecordingRules(ctx context.Context) ([]store.RecordingRule, error) {
	list, err := s.st.RecordingRules(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if g := list[i].Spec.Channel; g != "" {
			if it, err := s.st.ChannelForGroup(ctx, g); err == nil {
				list[i].ChannelName = it.Title
			}
		}
		list[i].LastNote = TranslateText(LangOf(ctx), list[i].LastNote) // saved in Italian
	}
	return list, nil
}

// RuleInput creates or changes a rule; Channel is a channel id (0: any).
type RuleInput struct {
	Name      string   `json:"name"`
	All       []string `json:"all"`
	Any       []string `json:"any"`
	None      []string `json:"none"`
	ChannelID int64    `json:"channel_id"`
	// Channel is the group key of a rule's channel, kept as it is when the
	// rule is changed without choosing the channel again (Sospendi).
	Channel  string `json:"channel"`
	Section  string `json:"section"`
	LiveOnly bool   `json:"live_only"`
	Keep     int    `json:"keep"`
	Enabled  *bool  `json:"enabled"`
}

func (s *Service) ruleFromInput(ctx context.Context, in RuleInput) (store.RecordingRule, error) {
	r := store.RecordingRule{Name: in.Name, Keep: in.Keep, Enabled: in.Enabled == nil || *in.Enabled,
		Spec: store.RuleSpec{All: in.All, Any: in.Any, None: in.None, Section: in.Section, LiveOnly: in.LiveOnly}}
	if in.ChannelID != 0 {
		g, err := s.st.GroupKeyOf(ctx, in.ChannelID)
		if err != nil {
			return r, invalid("canale inesistente")
		}
		r.Spec.Channel = g
	} else if in.Channel != "" {
		if _, err := s.st.ChannelForGroup(ctx, in.Channel); err != nil {
			return r, invalid("canale inesistente")
		}
		r.Spec.Channel = in.Channel
	}
	return r, s.checkRule(&r)
}

func (s *Service) PreviewRuleInput(ctx context.Context, in RuleInput) ([]RuleMatch, error) {
	r, err := s.ruleFromInput(ctx, in)
	if err != nil {
		return nil, err
	}
	return s.PreviewRule(ctx, r)
}

func (s *Service) AddRecordingRule(ctx context.Context, in RuleInput) (store.RecordingRule, error) {
	r, err := s.ruleFromInput(ctx, in)
	if err != nil {
		return r, err
	}
	if r.ID, err = s.st.AddRecordingRule(ctx, r); err != nil {
		return r, err
	}
	s.runRule(ctx, r.ID)
	return s.st.RecordingRule(ctx, r.ID)
}

func (s *Service) UpdateRecordingRule(ctx context.Context, id int64, in RuleInput) (store.RecordingRule, error) {
	r, err := s.ruleFromInput(ctx, in)
	if err != nil {
		return r, err
	}
	r.ID = id
	if err := s.st.UpdateRecordingRule(ctx, r); err != nil {
		return r, err
	}
	s.runRule(ctx, id)
	return s.st.RecordingRule(ctx, id)
}

func (s *Service) DeleteRecordingRule(ctx context.Context, id int64) error {
	return s.st.DeleteRecordingRule(ctx, id)
}

// runRule applies one rule now, so the user sees its effect at once.
func (s *Service) runRule(ctx context.Context, id int64) {
	r, err := s.st.RecordingRule(ctx, id)
	if err != nil || !r.Enabled {
		return
	}
	// The stored result is in Italian, like the periodic runs.
	ctx = WithLang(ctx, "it")
	note, err := s.applyRule(ctx, r)
	if err != nil {
		note = T(ctx, "errore: %s", err.Error())
	}
	s.st.SetRecordingRuleResult(ctx, id, note)
	s.scheduleDue(context.WithoutCancel(ctx))
}
