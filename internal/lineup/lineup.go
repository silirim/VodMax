// Package lineup recognises well-known national channels and gives them
// their official number, so the TV section can be browsed like a TV set.
package lineup

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

//go:embed it.json
var itJSON []byte

// Channel is a recognised national channel.
type Channel struct {
	Number  int      `json:"number"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

// Lineup maps normalised channel names to official channels.
type Lineup struct {
	Country string
	Source  string
	byName  map[string]Channel
}

// Italy returns the Italian digital terrestrial numbering.
func Italy() *Lineup {
	var raw struct {
		Country  string    `json:"country"`
		Source   string    `json:"source"`
		Channels []Channel `json:"channels"`
	}
	if err := json.Unmarshal(itJSON, &raw); err != nil {
		panic(err) // embedded data, checked by tests
	}
	l := &Lineup{Country: raw.Country, Source: raw.Source, byName: map[string]Channel{}}
	for _, c := range raw.Channels {
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			l.byName[Normalize(name)] = c
		}
	}
	return l
}

var (
	// Quality tags and resolution markers are not part of a channel's
	// identity; "+1" timeshift is, and is matched separately.
	tags     = regexp.MustCompile(`(?i)\b(?:4k|uhd|fhd|fullhd|superhd|hd|sd|hevc|h26[45]|1080p|720p|2160p|backup|ita|it)\b`)
	nonAlnum = regexp.MustCompile(`[^\p{L}\p{M}\p{N}+!.]+`) // every script, as catalog.GroupKey
	country  = regexp.MustCompile(`(?i)^\s*(?:it|ita|italia|italy)\s*[:|\-]\s*`)
)

// Normalize reduces a channel name to its matching form: lower case,
// no accents, no quality tags, no "IT:" country prefix.
func Normalize(name string) string {
	s := country.ReplaceAllString(name, "")
	s = strings.ToLower(norm.NFKD.String(s))
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, s)
	s = tags.ReplaceAllString(s, " ")
	s = nonAlnum.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// Match returns the official channel for a provider channel name.
// Timeshift (+1) channels are not matched.
func (l *Lineup) Match(name string) (Channel, bool) {
	key := Normalize(name)
	if strings.Contains(key, "+1") || strings.Contains(key, "+24") {
		return Channel{}, false
	}
	c, ok := l.byName[key]
	if !ok {
		c, ok = l.byName[strings.ReplaceAll(key, " ", "")]
	}
	return c, ok
}
