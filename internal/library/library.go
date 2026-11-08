// Package library reads a local collection: a folder of films or of
// series, named the way people (and Jellyfin, Kodi, Plex) usually name
// them. Titles and years come from the names; .nfo files and pictures
// next to the videos, when present, add plot, cast and artwork.
//
//	Films/Il Padrino (1972)/Il Padrino (1972).mkv   (or Films/Il Padrino (1972).mkv)
//	Serie/Twin Peaks/Stagione 1/Twin Peaks S01E03.mkv
package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/silirim/vodmax/internal/catalog"
	"github.com/silirim/vodmax/internal/store"
)

// Content is what a collection holds.
const (
	Movies = "movie"
	Series = "series"
)

// Category is the one category of a collection's items.
const Category = "local"

// Result is a scanned collection: items for the catalog and, for series,
// their episodes by the series' source id.
type Result struct {
	Items    []store.Item
	Episodes map[string][]store.Episode
	Skipped  int
}

// Item source ids and episode source ids are slash-separated paths
// relative to the collection's folder: Resolve turns them back into files.

// VideoExt are the video files of a collection; a device may copy the same
// ones (decision 0056).
var VideoExt = map[string]bool{"mkv": true, "mp4": true, "m4v": true, "avi": true, "mov": true, "wmv": true,
	"ts": true, "m2ts": true, "mts": true, "webm": true, "mpg": true, "mpeg": true, "flv": true, "ogm": true, "divx": true}

// Folders that hold extras, not the film or the episodes.
var extrasDir = map[string]bool{"extras": true, "featurettes": true, "behind the scenes": true, "deleted scenes": true,
	"interviews": true, "scenes": true, "shorts": true, "trailers": true, "sample": true, "samples": true, "other": true}

var sampleFile = regexp.MustCompile(`(?i)(^|[ ._-])(sample|trailer)([ ._-]|$)`)

// Scan reads the collection at root. A missing or unreadable root is an
// error (the catalog is then kept as it was); unreadable subfolders are
// skipped.
func Scan(root, content string) (*Result, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("cartella non raggiungibile: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("il percorso non è una cartella")
	}
	switch content {
	case Movies:
		return scanMovies(root)
	case Series:
		return scanSeries(root)
	}
	return nil, fmt.Errorf("contenuto %q non valido: film o serie", content)
}

// videos lists the video files under dir (relative to root), skipping
// hidden entries, extras and samples.
func videos(root, dir string) ([]string, int) {
	var out []string
	skipped := 0
	filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if extrasDir[strings.ToLower(name)] {
				return fs.SkipDir
			}
			return nil
		}
		if !VideoExt[ext(name)] {
			return nil
		}
		if sampleFile.MatchString(strings.TrimSuffix(name, filepath.Ext(name))) {
			skipped++
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out, skipped
}

func ext(name string) string { return strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) }

// ---- Films ----

func scanMovies(root string) (*Result, error) {
	files, skipped := videos(root, ".")
	// A film is named by its file; the folder's name is used instead when
	// the folder holds only that film and its name has the year the
	// file's lacks ("Il Padrino (1972)/film.mkv"), not for a generic
	// folder ("Vari/Heat (1995).mp4").
	perDir := map[string]int{}
	for _, f := range files {
		perDir[path.Dir(f)]++
	}
	res := &Result{Skipped: skipped}
	for _, f := range files {
		dir := path.Dir(f)
		name := strings.TrimSuffix(path.Base(f), path.Ext(f))
		if dir != "." && perDir[dir] == 1 {
			_, fileYear, _ := ParseName(name)
			if _, dirYear, _ := ParseName(path.Base(dir)); fileYear == 0 && dirYear != 0 {
				name = path.Base(dir)
			}
		}
		title, year, quality := ParseName(name)
		it := store.Item{Kind: "movie", SourceID: f, Title: name, DisplayTitle: title, Year: year,
			CategoryID: Category, Extension: ext(f), Quality: max(quality, qualityOf(f))}
		nfo := ReadNFO(filepath.Join(root, filepath.FromSlash(movieNFO(root, f))))
		applyNFO(&it, nfo)
		it.Image = imageRef(root, firstExisting(root, artwork(f, perDir[dir] == 1, "poster", "folder", "cover")))
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err == nil {
			it.AddedAt = st.ModTime().Unix()
		}
		it.GroupKey = catalog.GroupKey("movie", it.DisplayTitle, it.Year)
		it.LegacyKey = catalog.LegacyGroupKey("movie", it.DisplayTitle, it.Year)
		res.Items = append(res.Items, it)
	}
	return res, nil
}

// movieNFO: "<file>.nfo", else "movie.nfo" in the film's own folder.
func movieNFO(root, f string) string {
	own := strings.TrimSuffix(f, path.Ext(f)) + ".nfo"
	if exists(root, own) {
		return own
	}
	return path.Join(path.Dir(f), "movie.nfo")
}

// artwork lists the picture names to try for a video, in order:
// "<file>-poster.jpg", then the folder's "poster.jpg" when the folder is
// the video's own.
func artwork(f string, ownFolder bool, names ...string) []string {
	base := strings.TrimSuffix(f, path.Ext(f))
	var out []string
	for _, n := range names {
		for _, e := range []string{".jpg", ".jpeg", ".png", ".webp"} {
			out = append(out, base+"-"+n+e)
		}
	}
	if ownFolder {
		for _, n := range names {
			for _, e := range []string{".jpg", ".jpeg", ".png", ".webp"} {
				out = append(out, path.Join(path.Dir(f), n+e))
			}
		}
	}
	return out
}

// ---- Series ----

var (
	episodeCode = regexp.MustCompile(`(?i)\bS(\d{1,3})[ ._-]?E(\d{1,4})\b`)
	episodeX    = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	seasonDir   = regexp.MustCompile(`(?i)^(?:season|stagione|staffel|saison|temporada|sezon|s)[ ._-]*(\d{1,3})$`)
	episodeOnly = regexp.MustCompile(`(?i)\b(?:e|ep|episode|episodio|puntata|folge|odcinek)[ ._-]*(\d{1,4})\b`)
)

func scanSeries(root string) (*Result, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("cartella non leggibile: %w", err)
	}
	res := &Result{Episodes: map[string][]store.Episode{}}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") {
			continue
		}
		files, skipped := videos(root, name)
		res.Skipped += skipped
		if len(files) == 0 {
			continue
		}
		title, year, _ := ParseName(name)
		it := store.Item{Kind: "series", SourceID: name, Title: name, DisplayTitle: title, Year: year, CategoryID: Category,
			Quality: 2} // unknown, as for provider titles without a tag
		applyNFO(&it, ReadNFO(filepath.Join(root, name, "tvshow.nfo")))
		it.Image = imageRef(root, firstExisting(root, folderArt(name, "poster", "folder", "cover")))
		var newest int64
		eps := make([]store.Episode, 0, len(files))
		for _, f := range files {
			ep := parseEpisode(f, name)
			ep.Image = imageRef(root, firstExisting(root, artwork(f, false, "thumb"))) // Kodi and Jellyfin: "<file>-thumb.jpg"
			if nfo := ReadNFO(filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(f, path.Ext(f))+".nfo"))); nfo.Title != "" || nfo.Plot != "" {
				ep.Title = first(nfo.Title, ep.Title)
				ep.Plot = nfo.Plot
				if nfo.Runtime > 0 {
					ep.Duration = nfo.Runtime * 60
				}
			}
			eps = append(eps, ep)
			if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err == nil && st.ModTime().Unix() > newest {
				newest = st.ModTime().Unix()
			}
		}
		numberLoose(eps)
		sort.SliceStable(eps, func(a, b int) bool {
			if eps[a].Season != eps[b].Season {
				return eps[a].Season < eps[b].Season
			}
			return eps[a].Number < eps[b].Number
		})
		it.AddedAt = newest
		it.GroupKey = catalog.GroupKey("series", it.DisplayTitle, it.Year)
		it.LegacyKey = catalog.LegacyGroupKey("series", it.DisplayTitle, it.Year)
		res.Items = append(res.Items, it)
		res.Episodes[name] = eps
	}
	return res, nil
}

func folderArt(dir string, names ...string) []string {
	var out []string
	for _, n := range names {
		for _, e := range []string{".jpg", ".jpeg", ".png", ".webp"} {
			out = append(out, path.Join(dir, n+e))
		}
	}
	return out
}

// parseEpisode reads season and number from the file name ("S01E03",
// "1x03") or from a "Stagione 1" folder and "Episodio 3"; Number 0 means
// unknown (numbered later in name order).
func parseEpisode(f, series string) store.Episode {
	name := strings.TrimSuffix(path.Base(f), path.Ext(f))
	ep := store.Episode{SourceID: f, Extension: ext(f)}
	rest := ""
	if m := episodeCode.FindStringSubmatchIndex(name); m != nil {
		ep.Season, _ = strconv.Atoi(name[m[2]:m[3]])
		ep.Number, _ = strconv.Atoi(name[m[4]:m[5]])
		rest = name[m[1]:]
	} else if m := episodeX.FindStringSubmatchIndex(name); m != nil {
		ep.Season, _ = strconv.Atoi(name[m[2]:m[3]])
		ep.Number, _ = strconv.Atoi(name[m[4]:m[5]])
		rest = name[m[1]:]
	} else {
		rest = name // no code: the name is the title
		if m := episodeOnly.FindStringSubmatchIndex(name); m != nil {
			ep.Number, _ = strconv.Atoi(name[m[2]:m[3]])
			rest = name[m[1]:]
		}
		for dir := path.Dir(f); dir != "." && dir != series; dir = path.Dir(dir) {
			if m := seasonDir.FindStringSubmatch(path.Base(dir)); m != nil {
				ep.Season, _ = strconv.Atoi(m[1])
				break
			}
		}
	}
	if ep.Season == 0 {
		ep.Season = 1
	}
	// "Twin Peaks S01E03 - Zen, or the Skill to Catch a Killer" → the title.
	ep.Title = strings.Trim(cleanSeparators(rest), " -–:.")
	if t, _, _ := ParseName(ep.Title); t != "" {
		ep.Title = t
	}
	if ep.Title == "" || strings.EqualFold(ep.Title, series) {
		ep.Title = fmt.Sprintf("Episodio %d", max(ep.Number, 1))
	}
	return ep
}

// numberLoose gives files without a number the next free numbers of their
// season, in name order.
func numberLoose(eps []store.Episode) {
	used := map[[2]int]bool{}
	for _, e := range eps {
		if e.Number > 0 {
			used[[2]int{e.Season, e.Number}] = true
		}
	}
	for i := range eps {
		if eps[i].Number > 0 {
			continue
		}
		n := 1
		for used[[2]int{eps[i].Season, n}] {
			n++
		}
		eps[i].Number = n
		used[[2]int{eps[i].Season, n}] = true
		if strings.HasPrefix(eps[i].Title, "Episodio ") {
			eps[i].Title = fmt.Sprintf("Episodio %d", n)
		}
	}
}

// ---- Names ----

var (
	yearToken   = regexp.MustCompile(`[\[(]?\b(19\d{2}|20\d{2})\b[\])]?`)
	releaseTags = regexp.MustCompile(`(?i)\b(?:2160p|1080p|1080i|720p|576p|480p|4k|uhd|hdr10?|dv|bluray|blu-ray|bdrip|brrip|webrip|web-dl|webdl|web|hdtv|dvdrip|remux|x264|x265|h\.?264|h\.?265|hevc|avc|aac|ac3|dts|ddp?5\.1|atmos|10bit|ita|eng|multi|sub|subs|extended|director'?s cut|unrated|proper|repack)\b.*$`)
	bracketed   = regexp.MustCompile(`\[[^\]]*\]|\{[^}]*\}`)
)

// ParseName turns a file or folder name into title, year and quality:
// "The.Godfather.1972.1080p.BluRay.x264" → ("The Godfather", 1972, 3).
func ParseName(name string) (string, int, int) {
	quality := catalog.Quality(strings.NewReplacer(".", " ", "_", " ").Replace(name), catalog.CategoryInfo{})
	s := cleanSeparators(name)
	s = bracketed.ReplaceAllString(s, " ")
	year := 0
	// The last year in the name that is not the whole title ("1917",
	// "Blade Runner 2049 (2017)").
	if all := yearToken.FindAllStringSubmatchIndex(s, -1); len(all) > 0 {
		for i := len(all) - 1; i >= 0; i-- {
			m := all[i]
			if m[0] == 0 {
				continue
			}
			year, _ = strconv.Atoi(s[m[2]:m[3]])
			s = s[:m[0]]
			break
		}
	}
	s = releaseTags.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(strings.Trim(s, " -–._()[]")), " ")
	if s == "" {
		s = strings.TrimSpace(name)
	}
	return s, year, quality
}

// cleanSeparators: dots and underscores as spaces, when the name uses
// them instead of spaces.
func cleanSeparators(s string) string {
	if strings.Count(s, " ") < strings.Count(s, ".")+strings.Count(s, "_") {
		s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	}
	return s
}

func qualityOf(f string) int {
	_, _, q := ParseName(path.Base(f))
	return q
}

// ---- Files ----

// Resolve returns the file of a source id inside root, refusing ids that
// would leave it.
func Resolve(root, sourceID string) (string, error) {
	clean := path.Clean("/" + sourceID)
	if clean == "/" || strings.Contains(sourceID, "\x00") {
		return "", errors.New("percorso non valido")
	}
	return filepath.Join(root, filepath.FromSlash(clean[1:])), nil
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func firstExisting(root string, rels []string) string {
	for _, r := range rels {
		if exists(root, r) {
			return r
		}
	}
	return ""
}

// ImageScheme marks item images that are files of the collection.
const ImageScheme = "local:"

func imageRef(root, rel string) string {
	if rel == "" {
		return ""
	}
	return ImageScheme + rel
}

func first(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
