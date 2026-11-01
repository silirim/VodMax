package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/silirim/vodmax/internal/lineup"
	"github.com/silirim/vodmax/internal/playlist/m3u"
	"github.com/silirim/vodmax/internal/playlist/xtream"
	"github.com/silirim/vodmax/internal/store"
)

// Snapshot is a full catalog read from one subscription.
type Snapshot struct {
	Kinds      []string
	Categories []store.Category
	Items      []store.Item
	Account    json.RawMessage // provider account state, nil if unknown
	EPGURLs    []string
	Skipped    int // entries not imported (duplicates, unsupported)
}

// FromXtream reads account, categories, live channels, films and series.
// Episodes are loaded on demand when a series is opened. On error the
// snapshot may still carry the account state.
func FromXtream(ctx context.Context, c *xtream.Client) (*Snapshot, error) {
	acct, err := c.Login(ctx)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{Kinds: []string{"live", "movie", "series"}}
	snap.Account, _ = json.Marshal(map[string]any{
		"status":          acct.Status.String(),
		"active":          acct.Active(),
		"expires":         acct.Expires().Unix(),
		"max_connections": acct.MaxConnections.Int(),
		"active_cons":     acct.ActiveCons.Int(),
		"trial":           acct.IsTrial.Int() == 1,
	})
	if !acct.Active() {
		// The account state is still returned so it can be shown.
		return snap, fmt.Errorf("account del provider non attivo (stato %q)", acct.Status)
	}

	for kind, fetch := range map[string]func(context.Context) ([]xtream.Category, error){
		"live": c.LiveCategories, "movie": c.VODCategories, "series": c.SeriesCategories,
	} {
		cats, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		for _, cat := range cats {
			snap.Categories = append(snap.Categories, newCategory(kind, cat.ID.String(), cat.Name.String()))
		}
	}
	infos := categoryInfos(snap.Categories)

	seen := map[string]bool{}
	add := func(it store.Item) {
		finish(&it, infos[it.Kind+"\x00"+it.CategoryID])
		key := it.Kind + "\x00" + it.SourceID
		if it.SourceID == "" || seen[key] {
			snap.Skipped++
			return
		}
		seen[key] = true
		snap.Items = append(snap.Items, it)
	}

	live, err := c.LiveStreams(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range live {
		if IsSeparator(s.Name.String()) || !validEntry(s.Name, s.StreamID) {
			snap.Skipped++
			continue
		}
		catchup := 0
		if s.TVArchive.Int() == 1 {
			catchup = int(s.TVArchiveDuration.Int())
		}
		add(store.Item{
			Kind: "live", SourceID: s.StreamID.String(), Title: s.Name.String(), DisplayTitle: ChannelTitle(s.Name.String()),
			CategoryID: s.CategoryID.String(), Image: s.Icon.String(), EPGID: s.EPGChannelID.String(),
			Number: int(s.Num.Int()), CatchupDays: catchup, AddedAt: s.Added.Int(),
		})
	}

	vod, err := c.VODStreams(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range vod {
		if !validEntry(s.Name, s.StreamID) {
			snap.Skipped++
			continue
		}
		title, year := CleanTitle(s.Name.String())
		if y := int(s.Year.Int()); y >= 1900 && y <= 2100 {
			year = y
		}
		add(store.Item{
			Kind: "movie", SourceID: s.StreamID.String(), Title: s.Name.String(), DisplayTitle: title, Year: year,
			CategoryID: s.CategoryID.String(), Image: s.Icon.String(), Rating: parseRating(s.Rating.String()),
			TMDBID: tmdbID(s.TMDB.String()), Extension: s.ContainerExtension.String(), AddedAt: s.Added.Int(),
		})
	}

	series, err := c.Series(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range series {
		title, year := CleanTitle(s.Name.String())
		if year == 0 && len(s.ReleaseDate) >= 4 {
			if y, err := strconv.Atoi(string(s.ReleaseDate[:4])); err == nil && y >= 1900 && y <= 2100 {
				year = y
			}
		}
		add(store.Item{
			Kind: "series", SourceID: s.SeriesID.String(), Title: s.Name.String(), DisplayTitle: title, Year: year,
			CategoryID: s.CategoryID.String(), Image: s.Cover.String(), Rating: parseRating(s.Rating.String()),
			TMDBID: tmdbID(s.TMDB.String()),
		})
	}
	return snap, nil
}

func newCategory(kind, id, name string) store.Category {
	info := ClassifyCategory(kind, name)
	return store.Category{Kind: kind, SourceID: id, Name: name, Country: info.Country,
		Adult: info.Adult, LowRes: info.LowRes, Section: string(info.Section)}
}

func categoryInfos(cats []store.Category) map[string]CategoryInfo {
	out := make(map[string]CategoryInfo, len(cats))
	for _, c := range cats {
		out[c.Kind+"\x00"+c.SourceID] = CategoryInfo{Country: c.Country, Adult: c.Adult, LowRes: c.LowRes, Section: Section(c.Section)}
	}
	return out
}

var italy = lineup.Italy()

// finish computes the fields used by the catalog selection and the TV.
func finish(it *store.Item, info CategoryInfo) {
	it.Quality = Quality(it.Title, info)
	year := it.Year
	if it.Kind == "live" {
		year = 0
		it.NameKey = lineup.Normalize(it.DisplayTitle)
		// Official numbers only for home-country channels: a foreign
		// "Rai 1" relay must not take number 1.
		if info.Country == "" {
			if c, ok := italy.Match(it.DisplayTitle); ok {
				it.LCN = c.Number
			}
		}
	}
	it.GroupKey = GroupKey(it.Kind, it.DisplayTitle, year)
}

var trailingID = regexp.MustCompile(`^(\d+)\.[A-Za-z0-9]{1,5}$`)

// FromM3U converts a parsed playlist. Stream URLs often embed credentials,
// so they are sealed with seal(url, owner) and stored as locators.
// Episodes of series ("/series/" URLs) are not imported from M3U yet.
func FromM3U(p *m3u.Playlist, seal func(plain []byte, owner string) []byte, subID int64) *Snapshot {
	snap := &Snapshot{Kinds: []string{"live", "movie"}, EPGURLs: p.EPGURLs}
	groups := map[string]map[string]bool{"live": {}, "movie": {}}
	seen := map[string]bool{}
	for _, e := range p.Entries {
		kind, sourceID := classify(e)
		if kind == "" || IsSeparator(e.Name) {
			snap.Skipped++
			continue
		}
		key := kind + "\x00" + sourceID
		if seen[key] {
			snap.Skipped++
			continue
		}
		seen[key] = true
		it := store.Item{
			Kind: kind, SourceID: sourceID, Title: e.Name, DisplayTitle: ChannelTitle(e.Name),
			CategoryID: e.Group, Image: e.Logo, EPGID: e.TvgID, CatchupDays: e.CatchupDays,
			Extension: strings.TrimPrefix(path.Ext(urlPath(e.URL)), "."),
		}
		if kind == "movie" {
			it.DisplayTitle, it.Year = CleanTitle(e.Name)
		}
		finish(&it, ClassifyCategory(kind, e.Group))
		it.Locator = seal([]byte(e.URL), fmt.Sprintf("locator:%d:%s:%s", subID, kind, sourceID))
		snap.Items = append(snap.Items, it)
		if e.Group != "" && !groups[kind][e.Group] {
			groups[kind][e.Group] = true
			snap.Categories = append(snap.Categories, newCategory(kind, e.Group, e.Group))
		}
	}
	return snap
}

// classify decides kind and a source id that survives credential changes:
// the numeric stream id of Xtream-style URLs, else tvg-id, else a hash of
// name and group.
func classify(e m3u.Entry) (string, string) {
	p := urlPath(e.URL)
	kind := "live"
	switch {
	case strings.Contains(p, "/series/"):
		return "", ""
	case strings.Contains(p, "/movie/"), e.Duration > 0:
		kind = "movie"
	}
	if m := trailingID.FindStringSubmatch(path.Base(p)); m != nil {
		return kind, m[1]
	}
	if e.TvgID != "" && kind == "live" {
		return kind, "tvg:" + e.TvgID
	}
	sum := sha256.Sum256([]byte(e.Name + "\x00" + e.Group))
	return kind, "h:" + hex.EncodeToString(sum[:12])
}

func urlPath(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Path
	}
	return raw
}

func parseRating(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 || f > 10 {
		return 0
	}
	return f
}

func tmdbID(s string) string {
	s = strings.TrimSpace(s)
	if _, err := strconv.ParseUint(s, 10, 64); err != nil || s == "0" {
		return ""
	}
	return s
}

// validEntry leaves out entries with no real name or id (a broken line in
// the provider's list would become a channel that plays nothing).
func validEntry(name, id xtream.Flex) bool {
	n := strings.TrimSpace(name.String())
	return n != "" && n[0] != '[' && n[0] != '{' && id.Int() > 0
}
