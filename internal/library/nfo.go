package library

import (
	"encoding/xml"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/silirim/vodmax/internal/store"
)

// NFO is what VodMax uses of a Kodi/Jellyfin .nfo file (<movie>,
// <tvshow> or <episodedetails>).
type NFO struct {
	Title         string
	OriginalTitle string
	Year          int
	Plot          string
	Rating        float64
	Genres        []string
	Directors     []string
	Actors        []string
	Countries     []string
	Runtime       int // minutes
	Premiered     string
	TMDB          string
	Trailer       string // YouTube video id
}

type nfoXML struct {
	Title         string `xml:"title"`
	OriginalTitle string `xml:"originaltitle"`
	Year          string `xml:"year"`
	Plot          string `xml:"plot"`
	Outline       string `xml:"outline"`
	Rating        string `xml:"rating"`
	Ratings       []struct {
		Default string `xml:"default,attr"`
		Value   string `xml:"value"`
	} `xml:"ratings>rating"`
	Genres    []string `xml:"genre"`
	Directors []string `xml:"director"`
	Actors    []struct {
		Name string `xml:"name"`
	} `xml:"actor"`
	Countries []string `xml:"country"`
	Runtime   string   `xml:"runtime"`
	Premiered string   `xml:"premiered"`
	Aired     string   `xml:"aired"`
	TMDBID    string   `xml:"tmdbid"`
	UniqueIDs []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
	Trailer string `xml:"trailer"`
}

// ReadNFO reads an .nfo file; a missing or unreadable one is empty. Some
// tools append a URL after the XML: only the XML is read.
func ReadNFO(file string) NFO {
	f, err := os.Open(file)
	if err != nil {
		return NFO{}
	}
	defer f.Close()
	var raw nfoXML
	dec := xml.NewDecoder(io.LimitReader(f, 1<<20))
	dec.Strict = false
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	if err := dec.Decode(&raw); err != nil {
		return NFO{}
	}
	n := NFO{Title: strings.TrimSpace(raw.Title), OriginalTitle: strings.TrimSpace(raw.OriginalTitle),
		Plot: strings.TrimSpace(first(raw.Plot, raw.Outline)), Premiered: strings.TrimSpace(first(raw.Premiered, raw.Aired)),
		Genres: trimAll(raw.Genres), Directors: trimAll(raw.Directors), Countries: trimAll(raw.Countries),
		TMDB: strings.TrimSpace(raw.TMDBID), Trailer: youtubeID(raw.Trailer)}
	n.Year, _ = strconv.Atoi(strings.TrimSpace(raw.Year))
	if n.Year == 0 && len(n.Premiered) >= 4 {
		n.Year, _ = strconv.Atoi(n.Premiered[:4])
	}
	if n.Year < 1900 || n.Year > 2100 {
		n.Year = 0
	}
	n.Rating = rating(raw.Rating)
	for _, r := range raw.Ratings {
		if v := rating(r.Value); v > 0 && (n.Rating == 0 || r.Default == "true") {
			n.Rating = v
		}
	}
	n.Runtime, _ = strconv.Atoi(strings.TrimSpace(raw.Runtime))
	for _, a := range raw.Actors {
		if name := strings.TrimSpace(a.Name); name != "" && len(n.Actors) < 12 {
			n.Actors = append(n.Actors, name)
		}
	}
	for _, u := range raw.UniqueIDs {
		if strings.EqualFold(u.Type, "tmdb") && n.TMDB == "" {
			n.TMDB = strings.TrimSpace(u.Value)
		}
	}
	if _, err := strconv.ParseUint(n.TMDB, 10, 64); err != nil {
		n.TMDB = ""
	}
	return n
}

func rating(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 || f > 10 {
		return 0
	}
	return f
}

func trimAll(in []string) []string {
	var out []string
	for _, s := range in {
		// "Drama / Crime" in one tag, or one tag each.
		for _, part := range strings.Split(s, "/") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// youtubeID reads Kodi's "plugin://plugin.video.youtube/?action=play_video&videoid=ID"
// or a YouTube link.
func youtubeID(s string) string {
	s = strings.TrimSpace(s)
	for _, marker := range []string{"videoid=", "v=", "youtu.be/"} {
		if i := strings.Index(s, marker); i >= 0 {
			id := s[i+len(marker):]
			if j := strings.IndexAny(id, "&?#/"); j >= 0 {
				id = id[:j]
			}
			if len(id) == 11 {
				return id
			}
		}
	}
	return ""
}

func applyNFO(it *store.Item, n NFO) {
	if n.Title != "" {
		it.DisplayTitle = n.Title
	}
	if n.Year != 0 {
		it.Year = n.Year
	}
	if n.Rating > 0 {
		it.Rating = n.Rating
	}
	if n.TMDB != "" {
		it.TMDBID = n.TMDB
	}
}

// Details reads the .nfo of a film or series of the collection, and the
// relative path of its backdrop ("fanart"), "" if none.
func Details(root, kind, sourceID string) (NFO, string) {
	switch kind {
	case "movie":
		dir := path.Dir(sourceID)
		own := dir != "." && countVideos(root, dir) == 1
		fanart := firstExisting(root, artwork(sourceID, own, "fanart", "backdrop", "background"))
		return ReadNFO(filepath.Join(root, filepath.FromSlash(movieNFO(root, sourceID)))), fanart
	case "series":
		fanart := firstExisting(root, folderArt(sourceID, "fanart", "backdrop", "background"))
		return ReadNFO(filepath.Join(root, filepath.FromSlash(sourceID), "tvshow.nfo")), fanart
	}
	return NFO{}, ""
}

func countVideos(root, dir string) int {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && VideoExt[ext(e.Name())] && !sampleFile.MatchString(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))) {
			n++
		}
	}
	return n
}
