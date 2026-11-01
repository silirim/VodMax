// Package epg reads XMLTV programme guides.
package epg

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Channel is an XMLTV <channel>.
type Channel struct {
	ID    string
	Names []string
}

// Programme is an XMLTV <programme>; times are absolute.
type Programme struct {
	Channel     string
	Start, Stop time.Time
	Title       string
	Subtitle    string
	Description string
	Category    string
	Airing      string // live, rerun, premiere, new: what the guide says; "" nothing
}

// Guide is the part of an XMLTV document VodMax keeps.
type Guide struct {
	Channels   []Channel
	Programmes []Programme
}

// Filter decides which programmes are kept, so that a guide of several
// hundred MB never has to be held in memory.
type Filter func(channel string, start, stop time.Time) bool

// Options control parsing. OnChannel sees every <channel> before Keep is
// asked about its programmes (XMLTV lists channels first), so Keep can
// match channels by name.
type Options struct {
	OnChannel func(Channel)
	Keep      Filter
}

// Parse streams an XMLTV document. Channel ids are lower-cased.
func Parse(r io.Reader, opts Options) (*Guide, error) {
	keep := opts.Keep
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "us-ascii") {
			return input, nil
		}
		return nil, fmt.Errorf("codifica %q non supportata", charset)
	}
	g := &Guide{}
	// A programme without stop (optional in XMLTV) ends when the next one
	// of its channel starts.
	open := map[string]*Programme{}
	add := func(pr Programme) {
		if keep != nil && !keep(pr.Channel, pr.Start, pr.Stop) {
			return
		}
		g.Programmes = append(g.Programmes, pr)
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return g, nil
		}
		if err != nil {
			return g, fmt.Errorf("guida XMLTV: %w", err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch el.Name.Local {
		case "channel":
			var c struct {
				ID    string   `xml:"id,attr"`
				Names []string `xml:"display-name"`
			}
			if err := dec.DecodeElement(&c, &el); err != nil {
				return g, err
			}
			ch := Channel{ID: strings.ToLower(strings.TrimSpace(c.ID)), Names: c.Names}
			g.Channels = append(g.Channels, ch)
			if opts.OnChannel != nil {
				opts.OnChannel(ch)
			}
		case "programme":
			var p struct {
				Start    string    `xml:"start,attr"`
				Stop     string    `xml:"stop,attr"`
				Channel  string    `xml:"channel,attr"`
				Title    []string  `xml:"title"`
				SubTitle []string  `xml:"sub-title"`
				Desc     []string  `xml:"desc"`
				Category []string  `xml:"category"`
				Live     *struct{} `xml:"live"` // not in the DTD, used by some grabbers
				Previous *struct{} `xml:"previously-shown"`
				Premiere *struct{} `xml:"premiere"`
				New      *struct{} `xml:"new"`
			}
			if err := dec.DecodeElement(&p, &el); err != nil {
				return g, err
			}
			start, err := ParseTime(p.Start)
			if err != nil || len(p.Title) == 0 {
				continue
			}
			ch := strings.ToLower(strings.TrimSpace(p.Channel))
			if prev := open[ch]; prev != nil {
				delete(open, ch)
				if start.After(prev.Start) {
					prev.Stop = start
					add(*prev)
				}
			}
			pr := Programme{
				Channel: ch, Start: start, Title: strings.TrimSpace(p.Title[0]),
				Subtitle: first(p.SubTitle), Description: first(p.Desc), Category: first(p.Category),
				Airing: airing(p.Live != nil, p.Previous != nil, p.Premiere != nil, p.New != nil),
			}
			if strings.TrimSpace(p.Stop) == "" {
				open[ch] = &pr
				continue
			}
			stop, err := ParseTime(p.Stop)
			if err != nil || !stop.After(start) {
				continue
			}
			pr.Stop = stop
			add(pr)
		}
	}
}

func airing(live, previous, premiere, new bool) string {
	switch {
	case live:
		return "live"
	case previous:
		return "rerun"
	case premiere:
		return "premiere"
	case new:
		return "new"
	}
	return ""
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[0])
}

// ParseTime reads XMLTV times: "20261028113910 +0100", with or without
// seconds and offset (no offset means UTC); also written "…+0100",
// "… +01:00" or "…Z".
func ParseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	digits, offset, _ := strings.Cut(s, " ")
	if i := strings.IndexAny(digits, "+-Z"); i > 0 {
		digits, offset = digits[:i], digits[i:]
	}
	offset = strings.TrimSpace(offset)
	if offset == "Z" {
		offset = ""
	}
	if len(offset) == 6 && offset[3] == ':' {
		offset = offset[:3] + offset[4:]
	}
	switch len(digits) {
	case 14:
	case 12:
		digits += "00"
	default:
		return time.Time{}, fmt.Errorf("orario XMLTV non valido %q", s)
	}
	if offset == "" {
		return time.Parse("20060102150405", digits)
	}
	return time.Parse("20060102150405 -0700", digits+" "+offset)
}
