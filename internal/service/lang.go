package service

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Messages for people are written in Italian, as format strings; other
// languages translate those format strings in i18n/<lang>.json (it.json is
// the base that translators see on Weblate). A message without a
// translation stays in Italian.

//go:embed i18n/*.json
var messageFiles embed.FS

// Languages are the languages of the messages, Italian first.
var Languages = []string{"it"}

var catalogs = map[string]map[string]string{}

func init() {
	files, _ := fs.Glob(messageFiles, "i18n/*.json")
	for _, f := range files {
		code := strings.TrimSuffix(path.Base(f), ".json")
		if code == "it" {
			continue
		}
		raw, _ := messageFiles.ReadFile(f)
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			panic(f + ": " + err.Error())
		}
		for k, v := range m {
			if v == "" { // not translated yet
				delete(m, k)
			}
		}
		catalogs[code] = m
		Languages = append(Languages, code)
	}
}

type langKey struct{}

// WithLang attaches the language of a request ("it", "en"...).
func WithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langKey{}, lang)
}

// LangOf is the request's language; Italian for internal work.
func LangOf(ctx context.Context) string {
	if l, ok := ctx.Value(langKey{}).(string); ok {
		return l
	}
	return "it"
}

// PickLang chooses a supported language from a preference such as
// "en", "en-GB" or an Accept-Language header; "" if none fits.
func PickLang(pref string) string {
	for _, part := range strings.Split(pref, ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		base, _, _ := strings.Cut(strings.ToLower(tag), "-")
		for _, l := range Languages {
			if base == l {
				return l
			}
		}
	}
	return ""
}

// tr formats an Italian message in lang.
func tr(lang, format string, args ...any) string {
	if f, ok := catalogs[lang][format]; ok {
		format = f
	}
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// T formats an Italian message in the request's language.
func T(ctx context.Context, format string, args ...any) string {
	return tr(LangOf(ctx), format, args...)
}

// localizer is an error whose message is built from its fields.
type localizer interface{ localized(lang string) string }

// Localize returns the message of err for people speaking lang. A known
// whole text is translated; otherwise the outermost translatable error of
// the chain (input errors, errors with fields, known messages) has its
// text replaced, and the rest (e.g. a "guida: " prefix) stays in Italian.
func Localize(lang string, err error) string {
	msg := err.Error()
	if f, ok := catalogs[lang][msg]; ok {
		return f
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		var text string
		switch v := e.(type) {
		case ErrInvalid:
			if v.format == "" {
				continue
			}
			args := slices.Clone(v.args)
			for i, a := range args {
				if inner, ok := a.(error); ok {
					args[i] = Localize(lang, inner)
				}
			}
			text = tr(lang, v.format, args...)
		case localizer:
			text = v.localized(lang)
		default:
			f, ok := catalogs[lang][e.Error()]
			if !ok {
				continue
			}
			text = f
		}
		return strings.Replace(msg, e.Error(), text, 1)
	}
	return msg
}

// Stored texts (a sync error, a rule's note, a recording's error) are
// saved in Italian when they happen; TranslateText shows them in lang. A
// whole known text is translated; otherwise a format with %s, %d… that
// matches it (its values kept); otherwise, for "a: b" chains, each part.
func TranslateText(lang, s string) string {
	if lang == "it" || s == "" || catalogs[lang] == nil {
		return s
	}
	if f, ok := catalogs[lang][s]; ok {
		return f
	}
	if out, ok := translateFormatted(lang, s); ok {
		return out
	}
	if out, ok := translatePieces(lang, s); ok {
		return out
	}
	if head, rest, ok := strings.Cut(s, ": "); ok {
		return TranslateText(lang, head) + ": " + TranslateText(lang, rest)
	}
	return s
}

var verb = regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[sdvwqgf]`)

type pattern struct {
	re          *regexp.Regexp // the whole text
	prefix      *regexp.Regexp // the start of a text built from several messages
	format      string
	translation string
}

var patterns = map[string][]pattern{}
var patternsOnce sync.Once

func translateFormatted(lang, s string) (string, bool) {
	patternsOnce.Do(func() {
		for code, m := range catalogs {
			for k, v := range m {
				if !verb.MatchString(k) {
					continue
				}
				parts := verb.Split(k, -1)
				for i := range parts {
					parts[i] = regexp.QuoteMeta(parts[i])
				}
				body := strings.Join(parts, "(.+?)")
				re, err := regexp.Compile("^" + body + "$")
				// A format ending with a value takes the rest of the text.
				pre := "^" + body
				if strings.HasSuffix(pre, "(.+?)") {
					pre = strings.TrimSuffix(pre, "(.+?)") + "(.+)$"
				}
				prefix, err2 := regexp.Compile(pre)
				if err == nil && err2 == nil {
					patterns[code] = append(patterns[code], pattern{re, prefix, k, v})
				}
			}
			// The most specific format first.
			slices.SortFunc(patterns[code], func(a, b pattern) int { return len(b.format) - len(a.format) })
		}
	})
	for _, p := range patterns[lang] {
		m := p.re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		args := make([]any, len(m)-1)
		for i, a := range m[1:] {
			args[i] = TranslateText(lang, a)
		}
		// The values go back in as text, in order.
		i := 0
		return verb.ReplaceAllStringFunc(p.translation, func(string) string {
			if i >= len(args) {
				return ""
			}
			i++
			return fmt.Sprint(args[i-1])
		}), true
	}
	return "", false
}

// translatePieces translates a text made of messages one after the other
// ("2 in programma (1 nuove), 3 repliche saltate"); false unless every
// piece is a known message.
func translatePieces(lang string, s string) (string, bool) {
	translateFormatted(lang, "") // builds the patterns
	var out strings.Builder
	pieces := 0
	for rest := s; rest != ""; pieces++ {
		best, text := 0, ""
		for k, v := range catalogs[lang] {
			if len(k) > best && !verb.MatchString(k) && strings.HasPrefix(rest, k) {
				best, text = len(k), v
			}
		}
		for _, p := range patterns[lang] {
			m := p.prefix.FindStringSubmatch(rest)
			if m == nil || len(m[0]) <= best {
				continue
			}
			args := make([]any, len(m)-1)
			for i, a := range m[1:] {
				args[i] = TranslateText(lang, a)
			}
			i := 0
			best, text = len(m[0]), verb.ReplaceAllStringFunc(p.translation, func(string) string {
				if i >= len(args) {
					return ""
				}
				i++
				return fmt.Sprint(args[i-1])
			})
		}
		if best == 0 {
			return "", false
		}
		out.WriteString(text)
		rest = rest[best:]
	}
	return out.String(), pieces > 1
}
