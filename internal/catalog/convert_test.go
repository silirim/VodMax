package catalog

import (
	"strings"
	"testing"

	"github.com/silirim/vodmax/internal/playlist/m3u"
)

func TestFromM3U(t *testing.T) {
	playlist := `#EXTM3U url-tvg="http://epg.example/guide.xml"
#EXTINF:-1 tvg-id="rai1.it" group-title="Italia",Rai 1
http://provider.example/live/mario/secret/101.ts
#EXTINF:-1 tvg-id="rai1.it" group-title="Italia",Rai 1 (copia)
http://provider.example/live/mario/secret/101.ts
#EXTINF:-1 group-title="Film",1408 (2007)
http://provider.example/movie/mario/secret/555.mkv
#EXTINF:-1 group-title="Serie",Twin Peaks S01 E01
http://provider.example/series/mario/secret/9001.mp4
#EXTINF:-1 tvg-id="tg24.it" group-title="News",TG24
http://cdn.example/hls/tg24/index.m3u8?token=abc
#EXTINF:-1 tvg-id="tg24.it" group-title="News",TG24 FHD
http://cdn.example/hls/tg24fhd/index.m3u8?token=abc
#EXTINF:-1 tvg-id="tg24.it" group-title="News",TG24
http://cdn.example/hls/tg24/index.m3u8?token=abc
`
	p, err := m3u.Parse(strings.NewReader(playlist), m3u.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var owners []string
	seal := func(plain []byte, owner string) []byte {
		owners = append(owners, owner)
		return []byte("sealed")
	}
	snap := FromM3U(p, seal, 7)
	// Feeds sharing a tvg-id are each a channel; the same line twice is one.
	if len(snap.Items) != 4 || snap.Skipped != 3 {
		t.Fatalf("items %d skipped %d", len(snap.Items), snap.Skipped)
	}
	want := map[string]string{"101": "live", "555": "movie", "tvg:tg24.it": "live"}
	for _, it := range snap.Items {
		if it.Title == "TG24 FHD" && strings.HasPrefix(it.SourceID, "h:") {
			continue
		}
		if want[it.SourceID] != it.Kind {
			t.Errorf("unexpected item %s/%s", it.Kind, it.SourceID)
		}
		if string(it.Locator) != "sealed" || strings.Contains(it.Title, "secret") {
			t.Errorf("locator not sealed for %s", it.SourceID)
		}
		if it.SourceID == "555" && (it.DisplayTitle != "1408" || it.Year != 2007) {
			t.Errorf("movie title not cleaned: %+v", it)
		}
	}
	if owners[0] != "locator:7:live:101" {
		t.Errorf("locator owner %q", owners[0])
	}
	if len(snap.EPGURLs) != 1 || len(snap.Categories) != 3 {
		t.Errorf("epg %v categories %v", snap.EPGURLs, snap.Categories)
	}
}
