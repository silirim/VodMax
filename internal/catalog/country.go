package catalog

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Countries of provider categories (decision 0042). Providers write them
// in many ways: "Live Deutschland" (an Italian-market list, where home
// channels have plain names), "UK | Sport", "|DE| Filme", "FR: Cinéma",
// "[ES] Cine", "Italia 4K - FHD". Every spelling is brought to one code:
// ISO 3166 for countries (GB, not UK), and a few groups providers use
// for languages and regions (EN, ARABIC, EXYU, LATAM…). The interface
// shows the name in the viewer's language.

// countryAliases maps a normalised spelling (lower case, no accents) to
// its code. Two-letter codes that are also everyday words or quality tags
// (SD, HD, TV) are left out on purpose.
var countryAliases = map[string]string{}

func init() {
	for code, names := range map[string]string{
		"IT":      "it ita italy italia italian italiano italiani",
		"GB":      "uk gb gbr england united kingdom british britain inghilterra regno unito",
		"US":      "us usa united states america american stati uniti",
		"DE":      "de ger deu germany deutschland german deutsch germania",
		"AT":      "at aut austria osterreich asterreich",
		"CH":      "ch sui swiss switzerland schweiz suisse svizzera",
		"FR":      "fr fra france french francais francia",
		"ES":      "es esp spa spain espana spanish espanol spagna",
		"PT":      "pt por portugal portugues portuguese portogallo",
		"BR":      "br bra brazil brasil brasile",
		"NL":      "nl nld netherlands nederland dutch holland olanda",
		"BE":      "be bel belgium belgie belgique belgio",
		"PL":      "pl pol poland polska polski polish polonia",
		"RO":      "ro rou romania romanesc romanian",
		"AL":      "al alb albania shqip shqiptar shqiperia albanian",
		"GR":      "gr gre greece ellada hellas greek grecia",
		"TR":      "tr tur turkey turkiye turk turkish turchia",
		"BG":      "bg bul bulgaria bulgariya bulgarian",
		"RU":      "ru rus russia russkiye russian rossiya",
		"UA":      "ua ukr ukraine ukraina ukrainian",
		"SE":      "se swe sweden sverige swedish svezia",
		"NO":      "no nor norway norge norwegian norvegia",
		"DK":      "dk dnk denmark danmark danish danimarca",
		"FI":      "fi fin finland suomi finnish finlandia",
		"IE":      "ie irl ireland irish irlanda",
		"CA":      "ca can canada canadian",
		"AU":      "au aus australia australian",
		"IN":      "in ind india indian hindi",
		"PK":      "pk pak pakistan pakistani",
		"CZ":      "cz cze czech czechia cesko",
		"SK":      "sk svk slovakia slovensko",
		"HU":      "hu hun hungary magyar hungarian ungheria",
		"HR":      "hr hrv croatia hrvatska croatian",
		"RS":      "rs srb serbia srbija serbian",
		"SI":      "si svn slovenia slovenija",
		"BA":      "ba bih bosnia",
		"MK":      "mk mkd macedonia",
		"IL":      "il isr israel",
		"MX":      "mx mex mexico",
		"AR":      "argentina",
		"CL":      "cl chile",
		"CO":      "co colombia",
		"MA":      "ma morocco maroc marocco",
		"DZ":      "dz algeria algerie",
		"TN":      "tn tunisia tunisie",
		"EG":      "eg egypt egitto",
		"NG":      "ng nigeria",
		"ZA":      "za south africa sudafrica",
		"CN":      "cn china chinese cina",
		"JP":      "jp japan japanese giappone",
		"KR":      "kr korea korean corea",
		"TH":      "th thailand thai",
		"VN":      "vn vietnam",
		"PH":      "ph philippines filipino",
		"MT":      "mt malta",
		"CY":      "cy cyprus cipro",
		"LT":      "lt lithuania",
		"LV":      "lv latvia",
		"EE":      "ee estonia",
		"KU":      "kurdish kurdistan",
		"EN":      "en eng english inglese",
		"ARABIC":  "ar ara arab arabic alearab arabe",
		"EXYU":    "exyu ex-yu yu balkan balkans",
		"LATAM":   "latam latino latin america latinoamerica",
		"AFRICA":  "africa african",
		"ASIA":    "asia asian",
		"BENELUX": "benelux",
		"INT":     "int international internazionale",
	} {
		countryAliases[strings.ToLower(code)] = code
		for _, n := range splitAliases(names) {
			countryAliases[n] = code
		}
	}
}

// splitAliases splits an alias list on spaces, keeping the two-word names
// the table spells out ("united kingdom", "south africa").
func splitAliases(s string) []string {
	words := strings.Fields(s)
	out := []string{}
	for i := 0; i < len(words); i++ {
		if i+1 < len(words) {
			pair := words[i] + " " + words[i+1]
			if twoWord[pair] {
				out = append(out, pair)
				i++
				continue
			}
		}
		out = append(out, words[i])
	}
	return out
}

var twoWord = map[string]bool{"united kingdom": true, "regno unito": true, "united states": true, "stati uniti": true,
	"south africa": true, "latin america": true}

// fold lowers and strips accents: "España" → "espana".
func fold(s string) string {
	s = strings.ToLower(norm.NFKD.String(s))
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, s)
}

var countryNoise = regexp.MustCompile(`\b(?:new|select|vip|plus|premium|4k|uhd|fhd|hd|sd|hevc|channels?|tv)\b`)

// CanonicalCountry brings a country label to its code: "UK", "England" and
// "GB" are all "GB"; "Deutschland/Asterreich" is "DE". A label it does not
// know stays as it is, so labels saved by older versions still compare
// equal to themselves.
func CanonicalCountry(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	f := strings.TrimSpace(countryNoise.ReplaceAllString(fold(label), " "))
	if code, ok := countryAliases[f]; ok {
		return code
	}
	if code, ok := countryAliases[strings.Join(strings.Fields(f), " ")]; ok {
		return code
	}
	// "Deutschland/Asterreich", "UK & Ireland": the first one names it.
	if i := strings.IndexAny(f, "/&,+"); i > 0 {
		if code, ok := countryAliases[strings.TrimSpace(f[:i])]; ok {
			return code
		}
	}
	return label
}

var (
	// "UK | Sport", "|DE| Filme", "FR: Cinéma", "[ES] Cine", "IT - Rai",
	// "EX-YU ► Film": a code or a name, then a separator.
	codePrefix = regexp.MustCompile(`^[\s|\[({#*▎]*([\p{L}]{2,}(?:[ -][\p{L}]{2,})?)\s*[\])}]?\s*(?:[|:•►»▎/\]]|-\s|–|—)`)
	// "Action (IT)", "Cine [ES]".
	codeSuffix = regexp.MustCompile(`[\[(]\s*([\p{L}]{2,3})\s*[\])]\s*$`)
	firstWords = regexp.MustCompile(`^\s*([\p{L}]+)(?:\s+([\p{L}]+))?`)
)

// DetectCountry is the country a category name points to, in any of the
// spellings above, for channels, films and series alike. "" when the name
// names none: such categories follow the home country.
func DetectCountry(kind, name string) string {
	if kind == "live" {
		if m := foreignPrefix.FindStringSubmatch(name); m != nil {
			return CanonicalCountry(m[1])
		}
	}
	if m := codePrefix.FindStringSubmatch(name); m != nil {
		cand := fold(m[1])
		if code, ok := countryAliases[cand]; ok {
			return code
		}
		// "EX YU | Film" and "Ex-Yu" spell the same group.
		if code, ok := countryAliases[strings.ReplaceAll(strings.ReplaceAll(cand, " ", ""), "-", "")]; ok {
			return code
		}
	}
	if m := codeSuffix.FindStringSubmatch(name); m != nil {
		if code, ok := countryAliases[fold(m[1])]; ok {
			return code
		}
	}
	// "Italia", "Italia 4K - FHD", "Germany Sports": a full name (four
	// letters or more, so not a code that is also a word) first.
	if m := firstWords.FindStringSubmatch(name); m != nil {
		if m[2] != "" {
			if code, ok := countryAliases[fold(m[1]+" "+m[2])]; ok && twoWord[fold(m[1]+" "+m[2])] {
				return code
			}
		}
		if w := fold(m[1]); len([]rune(w)) >= 4 {
			if code, ok := countryAliases[w]; ok {
				return code
			}
		}
	}
	if international.MatchString(name) {
		return "INT"
	}
	return ""
}
