package m3u

import (
	"errors"
	"strings"
	"testing"
)

const sample = "\uFEFF#EXTM3U url-tvg=\"http://epg.example/a.xml, http://epg.example/b.xml\"\n" +
	`#EXTINF:-1 tvg-id="rai1.it" tvg-name="Rai 1" tvg-logo="http://img.example/rai1.png" group-title="Italia, HD" catchup="default" catchup-days="7",Rai 1 HD
#EXTVLCOPT:http-user-agent=Player/1.0
http://provider.example/live/1.ts

#EXTINF:7200 group-title="Film",Alba, tramonto (2019)
#EXTGRP:Ignorato perché c'è group-title
http://provider.example/movie/2.mkv
#EXTINF:-1,Senza attributi
#EXTGRP:Sport
http://provider.example/live/3.ts
http://provider.example/live/4.ts
`

func TestParse(t *testing.T) {
	p, err := Parse(strings.NewReader(sample), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.EPGURLs) != 2 || p.EPGURLs[1] != "http://epg.example/b.xml" {
		t.Fatalf("epg urls: %v", p.EPGURLs)
	}
	if len(p.Entries) != 4 {
		t.Fatalf("entries: %d", len(p.Entries))
	}
	e := p.Entries[0]
	if e.Name != "Rai 1 HD" || e.TvgID != "rai1.it" || e.Group != "Italia, HD" || e.UserAgent != "Player/1.0" ||
		e.Catchup != "default" || e.CatchupDays != 7 || e.Duration != -1 {
		t.Fatalf("first entry: %+v", e)
	}
	if m := p.Entries[1]; m.Name != "Alba, tramonto (2019)" || m.Duration != 7200 || m.Group != "Film" {
		t.Fatalf("comma in name or group: %+v", m)
	}
	if s := p.Entries[2]; s.Group != "Sport" || s.Name != "Senza attributi" {
		t.Fatalf("EXTGRP: %+v", s)
	}
	if bare := p.Entries[3]; bare.Name != bare.URL || bare.Duration != -1 {
		t.Fatalf("bare url: %+v", bare)
	}
}

func TestLimits(t *testing.T) {
	_, err := Parse(strings.NewReader(sample), Limits{MaxEntries: 2, MaxLineLength: 1024})
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("expected ErrTooManyEntries, got %v", err)
	}
	long := "#EXTM3U\n#EXTINF:-1," + strings.Repeat("x", 2048) + "\nhttp://a\n"
	if _, err := Parse(strings.NewReader(long), Limits{MaxEntries: 10, MaxLineLength: 1024}); err == nil {
		t.Fatal("expected error for overlong line")
	}
}

// A web page (a provider's error or login page) is not a playlist, and
// lines that are not addresses are not entries.
func TestNotAPlaylist(t *testing.T) {
	if _, err := Parse(strings.NewReader("<html><body>not a playlist</body></html>"), Limits{}); !errors.Is(err, ErrNotPlaylist) {
		t.Fatalf("web page: %v", err)
	}
	p, err := Parse(strings.NewReader("#EXTM3U\n#EXTINF:-1,Uno\nhttp://x/1.ts\nsolo testo\n#EXTINF:-1,Due\n/locale/2.ts\n"), Limits{})
	if err != nil || len(p.Entries) != 2 {
		t.Fatalf("entries: %+v %v", p, err)
	}
}
