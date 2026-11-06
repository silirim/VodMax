package catalog

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Section is where live channels appear in the user interface.
type Section string

const (
	SectionTV    Section = "tv"    // linear channels: Rai 1, Canale 5, Sky Cinema…
	SectionSport Section = "sport" // sport channels and per-event feeds
	SectionRadio Section = "radio"
)

// CategoryInfo is what VodMax infers from a provider category name. The
// user can override every field.
type CategoryInfo struct {
	Country string  // "" = the subscription's home country
	Adult   bool    // adult content
	LowRes  bool    // a category of low-quality duplicates (e.g. "SD (Connessioni Lente)")
	Section Section // live categories only
}

var (
	// Providers group foreign channels as "Live <Country>" or under an
	// "International" heading; home-country channels have plain names.
	foreignPrefix = regexp.MustCompile(`(?i)^\s*(?:live|tv)\s*[:|\-]?\s+(.+?)\s*(?:new|select)?\s*$`)
	international = regexp.MustCompile(`(?i)\binternational\b`)
	adultWords    = regexp.MustCompile(`(?i)\b(?:xxx|adult[io]?s?|porn\w*|18\s*\+|erotic\w*|hot\s+club)\b`)
	lowResWords   = regexp.MustCompile(`(?i)\bSD\b.*(?:lent|slow)|connessioni\s+lente|low\s+bandwidth`)
	sportWords    = regexp.MustCompile(`(?i)\b(?:sport\w*|calcio|serie\s+[abc]\b|champions|dazn|f1|formula\s*1|motogp|basket|nba|nfl|tennis|volley|pallamano|eurosport|ufc|wwe|boxe|golf|rugby|ciclismo|zona\s+gol)\b`)
	radioWords    = regexp.MustCompile(`(?i)\bradio\b`)
	// homeWords are what follows "Live"/"TV" in groups of home channels.
	homeWords = regexp.MustCompile(`(?i)^(?:events?|eventi|ppv|locali|regionali)\b`)
)

// liveCountry is the country label of a "Live <X>" / "TV <X>" group. On
// Italian lists "Live Calcio", "LIVE EVENTS" and "TV Locali" are home
// groups, so an X that names no known country and reads as sport, radio
// or local channels is none (ok false). Any other unknown X stays a
// country as written: a foreign group whose spelling the table lacks must
// not show up by mistake.
func liveCountry(name string) (string, bool) {
	m := foreignPrefix.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	label := strings.TrimSpace(m[1])
	if CanonicalCountry(label) == label && (sportWords.MatchString(label) || radioWords.MatchString(label) || homeWords.MatchString(label)) {
		return "", false
	}
	return label, true
}

// ClassifyCategory infers country, adult flag, quality tier and section
// from a category name.
func ClassifyCategory(kind, name string) CategoryInfo {
	info := CategoryInfo{Adult: adultWords.MatchString(name), LowRes: lowResWords.MatchString(name)}
	if c, ok := liveCountry(name); ok && kind == "live" {
		info.Country = c
	} else if international.MatchString(name) {
		info.Country = "Internazionale"
	}
	if kind == "live" {
		switch {
		case radioWords.MatchString(name):
			info.Section = SectionRadio
		case sportWords.MatchString(name):
			info.Section = SectionSport
		default:
			info.Section = SectionTV
		}
	}
	return info
}

// Quality ranks a variant from its title and category: 4 = 4K/UHD,
// 3 = FHD/1080p, 2 = HD/720p or unspecified, 1 = SD.
func Quality(title string, info CategoryInfo) int {
	t := strings.ToUpper(title)
	switch {
	case containsWord(t, "4K", "UHD", "2160P"):
		return 4
	case containsWord(t, "FHD", "1080P", "FULLHD", "SUPERHD"):
		return 3
	case info.LowRes || containsWord(t, "SD"):
		return 1
	}
	return 2
}

func containsWord(upper string, words ...string) bool {
	fields := strings.FieldsFunc(upper, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, f := range fields {
		for _, w := range words {
			if f == w {
				return true
			}
		}
	}
	return false
}

var (
	qualityTags = regexp.MustCompile(`(?i)\b(?:4k|uhd|fhd|fullhd|superhd|hd|sd|hevc|h26[45]|2160p|1080p|720p|ita|sub\s*ita|multi(?:sub)?)\b|\[[^\]]*\]`)
	nonAlnum    = regexp.MustCompile(`[^a-z0-9+]+`)
)

// GroupKey identifies variants of the same channel or title: case, accents,
// punctuation and quality tags are ignored; for films and series the year
// is part of the key. "+1" timeshift channels stay distinct.
func GroupKey(kind, displayTitle string, year int) string {
	s := strings.ToLower(norm.NFKD.String(displayTitle))
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, s)
	s = qualityTags.ReplaceAllString(s, " ")
	s = strings.TrimSpace(nonAlnum.ReplaceAllString(s, " "))
	if kind != "live" && year > 0 {
		return s + "|" + itoa(year)
	}
	return s
}

func itoa(n int) string {
	var b [11]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
