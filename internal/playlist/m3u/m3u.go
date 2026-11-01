// Package m3u parses extended M3U playlists as published by playlist providers.
package m3u

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Entry is one playable item of a playlist.
type Entry struct {
	Name        string
	URL         string
	TvgID       string
	TvgName     string
	Logo        string
	Group       string
	Duration    int // seconds, -1 for live
	UserAgent   string
	Referrer    string
	Catchup     string // catchup attribute, e.g. "default" or "append"
	CatchupDays int
	Attrs       map[string]string
}

// Playlist is a parsed M3U document.
type Playlist struct {
	// EPGURLs come from url-tvg / x-tvg-url in the header.
	EPGURLs []string
	Entries []Entry
}

// Limits bound memory use on hostile or broken playlists.
type Limits struct {
	MaxEntries    int
	MaxLineLength int
}

// DefaultLimits fit the largest real provider lists seen (~250k entries).
var DefaultLimits = Limits{MaxEntries: 2_000_000, MaxLineLength: 64 * 1024}

// ErrNotPlaylist means the answer is a web page or another document.
var ErrNotPlaylist = errors.New("la risposta non è una playlist M3U (sembra una pagina web o un altro documento)")

// ErrTooManyEntries means the playlist exceeded Limits.MaxEntries.
var ErrTooManyEntries = errors.New("playlist troppo grande")

// Parse reads an extended M3U playlist.
func Parse(r io.Reader, limits Limits) (*Playlist, error) {
	if limits.MaxEntries == 0 {
		limits = DefaultLimits
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, min(4096, limits.MaxLineLength)), limits.MaxLineLength)
	p := &Playlist{}
	var pending *Entry
	first := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if first {
			line = strings.TrimPrefix(line, "\uFEFF")
			first = false
		}
		switch {
		case line == "":
		case strings.HasPrefix(line, "<") && len(p.Entries) == 0 && pending == nil:
			// A web page or an XML document (a login page, an XMLTV guide
			// given as the playlist), not a playlist.
			return nil, ErrNotPlaylist
		case strings.HasPrefix(line, "#EXTM3U"):
			attrs, _ := parseAttrs(strings.TrimPrefix(line, "#EXTM3U"))
			for _, key := range []string{"url-tvg", "x-tvg-url"} {
				for _, u := range strings.Split(attrs[key], ",") {
					if u = strings.TrimSpace(u); u != "" {
						p.EPGURLs = append(p.EPGURLs, u)
					}
				}
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			e := parseExtinf(strings.TrimPrefix(line, "#EXTINF:"))
			pending = &e
		case strings.HasPrefix(line, "#EXTGRP:"):
			if pending != nil && pending.Group == "" {
				pending.Group = strings.TrimSpace(strings.TrimPrefix(line, "#EXTGRP:"))
			}
		case strings.HasPrefix(line, "#EXTVLCOPT:"):
			if pending != nil {
				key, value, _ := strings.Cut(strings.TrimPrefix(line, "#EXTVLCOPT:"), "=")
				switch strings.ToLower(strings.TrimSpace(key)) {
				case "http-user-agent":
					pending.UserAgent = strings.TrimSpace(value)
				case "http-referrer", "http-referer":
					pending.Referrer = strings.TrimSpace(value)
				}
			}
		case strings.HasPrefix(line, "#"):
			// Other directives (#KODIPROP, #EXT-X-...) are not needed.
		case !strings.Contains(line, "://") && !strings.HasPrefix(line, "/"):
			pending = nil // not an address: not an entry
		default:
			e := Entry{Duration: -1}
			if pending != nil {
				e = *pending
			}
			e.URL = line
			if e.Name == "" {
				e.Name = line
			}
			if len(p.Entries) >= limits.MaxEntries {
				return nil, ErrTooManyEntries
			}
			p.Entries = append(p.Entries, e)
			pending = nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("lettura playlist: %w", err)
	}
	return p, nil
}

func parseExtinf(s string) Entry {
	e := Entry{Duration: -1}
	// Format: <duration> key="value" ...,<name>. The name may contain commas,
	// quoted values may too: find the first comma outside quotes.
	inQuote, split := false, -1
	for i, r := range s {
		if r == '"' {
			inQuote = !inQuote
		} else if r == ',' && !inQuote {
			split = i
			break
		}
	}
	head := s
	if split >= 0 {
		head, e.Name = s[:split], strings.TrimSpace(s[split+1:])
	}
	durationText, rest, _ := strings.Cut(strings.TrimSpace(head), " ")
	fmt.Sscanf(durationText, "%d", &e.Duration)
	e.Attrs, _ = parseAttrs(rest)
	e.TvgID = e.Attrs["tvg-id"]
	e.TvgName = e.Attrs["tvg-name"]
	e.Logo = e.Attrs["tvg-logo"]
	e.Group = e.Attrs["group-title"]
	e.Catchup = e.Attrs["catchup"]
	fmt.Sscanf(e.Attrs["catchup-days"], "%d", &e.CatchupDays)
	if e.Name == "" {
		e.Name = e.TvgName
	}
	return e
}

// parseAttrs reads key="value" pairs; keys are lower-cased.
func parseAttrs(s string) (map[string]string, error) {
	attrs := map[string]string{}
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return attrs, nil
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return attrs, errors.New("attributo senza valore")
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		var value string
		if strings.HasPrefix(s, `"`) {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				value, s = s[1:], ""
			} else {
				value, s = s[1:end+1], s[end+2:]
			}
		} else {
			value, s, _ = strings.Cut(s, " ")
		}
		attrs[key] = value
	}
}
