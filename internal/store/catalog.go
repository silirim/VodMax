package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Item is one catalog entry as imported from a provider.
type Item struct {
	Kind         string
	SourceID     string
	Title        string
	DisplayTitle string
	Year         int
	CategoryID   string
	Image        string
	EPGID        string
	Number       int
	Rating       float64
	TMDBID       string
	Extension    string
	CatchupDays  int
	AddedAt      int64
	Locator      []byte // sealed stream URL, M3U only
	Quality      int    // 1 SD … 4 UHD
	GroupKey     string // identifies variants of the same title or channel
	LCN          int    // official channel number, 0 if not recognised
	NameKey      string // normalised channel name, to match the guide
}

// Category groups items of one kind inside a subscription, with what
// VodMax inferred from its name.
type Category struct {
	Kind     string `json:"kind"`
	SourceID string `json:"id"`
	Name     string `json:"name"`
	Country  string `json:"country,omitempty"`
	Adult    bool   `json:"adult,omitempty"`
	LowRes   bool   `json:"low_res,omitempty"`
	Section  string `json:"section,omitempty"`
}

// ImportResult summarises a catalog replacement.
type ImportResult struct {
	Total   map[string]int `json:"total"`
	Added   int            `json:"added"`
	Removed int            `json:"removed"`
}

// ErrSuspiciousDrop protects the catalog from broken provider responses:
// the previous catalog stays available.
var ErrSuspiciousDrop = errors.New("il nuovo catalogo ha meno della metà dei titoli: mantenuto il precedente")

const dropMinimum = 50

// ReplaceCatalog makes items the current content of the given kinds of a
// subscription, in a single write transaction. Existing rows keep their id
// (and so favourites and progress); missing ones become unavailable, never
// deleted. Readers keep seeing the previous catalog until commit.
func (s *Store) ReplaceCatalog(ctx context.Context, subID int64, kinds []string, categories []Category, items []Item, allowDrop bool) (ImportResult, error) {
	res := ImportResult{Total: map[string]int{}}
	covered := map[string]bool{}
	for _, k := range kinds {
		covered[k] = true
		res.Total[k] = 0
	}
	for _, it := range items {
		if !covered[it.Kind] {
			return res, fmt.Errorf("tipo %q non dichiarato nell'importazione", it.Kind)
		}
		res.Total[it.Kind]++
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	for _, kind := range kinds {
		var current int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM item WHERE subscription_id = ? AND kind = ? AND available = 1`,
			subID, kind).Scan(&current); err != nil {
			return res, err
		}
		if !allowDrop && current >= dropMinimum && res.Total[kind]*2 < current {
			return res, fmt.Errorf("%w (%s: %d → %d)", ErrSuspiciousDrop, kind, current, res.Total[kind])
		}
	}

	// Keys that change in this import are noted, to carry over what is
	// kept by group key (see carryGroupKeys).
	for _, q := range []string{
		`CREATE TEMP TABLE IF NOT EXISTS moved_item (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, old TEXT NOT NULL)`,
		`CREATE TEMP TRIGGER IF NOT EXISTS item_key_moved AFTER UPDATE OF group_key ON item
			WHEN old.group_key <> new.group_key AND new.group_key <> ''
			BEGIN INSERT OR IGNORE INTO moved_item VALUES (old.id, old.kind, old.group_key); END`,
		`DELETE FROM temp.moved_item`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return res, err
		}
	}

	now := time.Now().UnixNano() // unique per import: identifies rows seen now
	// New rows start hidden and the selection that follows the import
	// shows them: if it fails or the process stops in between, a title of
	// an adult category or an excluded country is not open to every
	// profile, kids included, until the next selection.
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO item (subscription_id, kind, source_id, title, display_title, year, category_id, image, epg_id,
			number, rating, tmdb_id, extension, catchup_days, added_at, locator, first_seen_at, last_seen_at, available,
			quality, group_key, lcn, name_key, visible)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, 0)
		ON CONFLICT (subscription_id, kind, source_id) DO UPDATE SET
			title = excluded.title, display_title = excluded.display_title, year = excluded.year,
			category_id = excluded.category_id, image = excluded.image, epg_id = excluded.epg_id,
			number = excluded.number, rating = excluded.rating, tmdb_id = excluded.tmdb_id,
			extension = excluded.extension, catchup_days = excluded.catchup_days, added_at = excluded.added_at,
			locator = excluded.locator, last_seen_at = excluded.last_seen_at,
			available = 1, quality = excluded.quality, group_key = excluded.group_key,
			lcn = excluded.lcn, name_key = excluded.name_key
		RETURNING first_seen_at`)
	if err != nil {
		return res, err
	}
	defer stmt.Close()
	for _, it := range items {
		var first int64
		err := stmt.QueryRowContext(ctx, subID, it.Kind, it.SourceID, it.Title, it.DisplayTitle, zeroNull(it.Year),
			it.CategoryID, it.Image, it.EPGID, zeroNull(it.Number), zeroNullF(it.Rating), it.TMDBID, it.Extension,
			it.CatchupDays, zeroNull64(it.AddedAt), it.Locator, now, now, max(it.Quality, 1), it.GroupKey, zeroNull(it.LCN), it.NameKey).Scan(&first)
		if err != nil {
			return res, fmt.Errorf("titolo %q: %w", it.Title, err)
		}
		if first == now {
			res.Added++
		}
	}

	for _, kind := range kinds {
		r, err := tx.ExecContext(ctx, `UPDATE item SET available = 0 WHERE subscription_id = ? AND kind = ? AND available = 1 AND last_seen_at <> ?`,
			subID, kind, now)
		if err != nil {
			return res, err
		}
		n, _ := r.RowsAffected()
		res.Removed += int(n)
		if _, err := tx.ExecContext(ctx, `DELETE FROM category WHERE subscription_id = ? AND kind = ?`, subID, kind); err != nil {
			return res, err
		}
	}
	if err := carryGroupKeys(ctx, tx); err != nil {
		return res, err
	}
	for _, c := range categories {
		if !covered[c.Kind] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO category (subscription_id, kind, source_id, name, country, adult, low_res, section)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, subID, c.Kind, c.SourceID, c.Name, c.Country, c.Adult, c.LowRes, c.Section); err != nil {
			return res, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO item_search(item_search) VALUES ('rebuild')`); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	// A full import rewrites most pages: fold the WAL back into the main
	// file so it does not stay at catalog size. Best effort, readers may
	// still hold old snapshots.
	s.w.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return res, nil
}

// carryGroupKeys moves what is kept by group key (favourites, titles
// taken off "continue watching", series markers, rule channels and
// recordings) to the new key of the titles whose key changed in this
// import: a provider renaming a title, or a newer VodMax computing keys
// differently (0.6.7 keeps non-Latin letters, which older versions
// dropped, so "Первый канал" and "الجزيرة" were both ""). Only a key left
// on no available title moves; an old key that was shared by several
// titles moves to all of them, as it applied to all of them before.
func carryGroupKeys(ctx context.Context, tx *sql.Tx) error {
	var moved bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM temp.moved_item)`).Scan(&moved); err != nil || !moved {
		return err
	}
	for _, q := range []string{
		`CREATE TEMP TABLE IF NOT EXISTS moved_key (kind TEXT NOT NULL, old TEXT NOT NULL, new TEXT NOT NULL)`,
		`DELETE FROM temp.moved_key`,
		`INSERT INTO temp.moved_key SELECT DISTINCT m.kind, m.old, i.group_key FROM temp.moved_item m JOIN item i ON i.id = m.id
			WHERE NOT EXISTS (SELECT 1 FROM item v WHERE v.kind = m.kind AND v.group_key = m.old AND v.available = 1)`,
		`INSERT OR IGNORE INTO favorite (profile_id, kind, group_key, position, added_at)
			SELECT f.profile_id, f.kind, m.new, f.position, f.added_at FROM favorite f JOIN temp.moved_key m ON m.kind = f.kind AND m.old = f.group_key`,
		`DELETE FROM favorite WHERE EXISTS (SELECT 1 FROM temp.moved_key m WHERE m.kind = favorite.kind AND m.old = favorite.group_key)`,
		`INSERT OR IGNORE INTO continue_hidden (profile_id, title_key, hidden_at)
			SELECT h.profile_id, m.kind || ':' || m.new, h.hidden_at FROM continue_hidden h JOIN temp.moved_key m ON h.title_key = m.kind || ':' || m.old`,
		`DELETE FROM continue_hidden WHERE title_key IN (SELECT kind || ':' || old FROM temp.moved_key)`,
		`INSERT OR IGNORE INTO series_marker (series_key, intro_start, intro_end, credits, updated_at)
			SELECT m.new, s.intro_start, s.intro_end, s.credits, s.updated_at FROM series_marker s JOIN temp.moved_key m ON m.kind = 'series' AND m.old = s.series_key`,
		`DELETE FROM series_marker WHERE series_key IN (SELECT old FROM temp.moved_key WHERE kind = 'series')`,
		// A recording follows its own channel.
		`UPDATE recording SET group_key = (SELECT i.group_key FROM item i WHERE i.id = recording.item_id)
			WHERE item_id IN (SELECT id FROM temp.moved_item)`,
		// A rule follows the channel it last recorded, else the only one
		// its old key leads to; "" means any channel and stays.
		`UPDATE recording_rule SET spec = json_set(spec, '$.channel', t.new) FROM (
			SELECT r.id, coalesce(
				(SELECT rec.group_key FROM recording rec JOIN temp.moved_key m ON m.kind = 'live' AND m.new = rec.group_key
					WHERE rec.rule_id = r.id AND m.old = json_extract(r.spec, '$.channel') ORDER BY rec.start DESC LIMIT 1),
				(SELECT min(m.new) FROM temp.moved_key m WHERE m.kind = 'live' AND m.old = json_extract(r.spec, '$.channel') HAVING count(*) = 1)) AS new
			FROM recording_rule r WHERE json_extract(r.spec, '$.channel') IN (SELECT old FROM temp.moved_key WHERE kind = 'live' AND old <> '')
		) t WHERE t.id = recording_rule.id AND t.new IS NOT NULL`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("chiavi dei titoli: %w", err)
		}
	}
	return nil
}

func zeroNull(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func zeroNull64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func zeroNullF(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

// CatalogItem is the public view of an item: no locator.
type CatalogItem struct {
	ID             int64    `json:"id"`
	SubscriptionID int64    `json:"subscription_id"`
	Kind           string   `json:"kind"`
	SourceID       string   `json:"source_id"`
	Title          string   `json:"title"`
	DisplayTitle   string   `json:"display_title"`
	Year           *int     `json:"year,omitempty"`
	CategoryID     string   `json:"category_id,omitempty"`
	Image          string   `json:"image,omitempty"`
	EPGID          string   `json:"epg_id,omitempty"`
	Number         *int     `json:"number,omitempty"`
	Rating         *float64 `json:"rating,omitempty"`
	TMDBID         string   `json:"tmdb_id,omitempty"`
	Extension      string   `json:"-"`
	CatchupDays    int      `json:"catchup_days,omitempty"`
	FirstSeenAt    int64    `json:"first_seen_at"`
	Available      bool     `json:"available"`
	Quality        int      `json:"quality"`
	Section        string   `json:"section,omitempty"`
	Visible        bool     `json:"visible"`
	VariantCount   int      `json:"variant_count"`
	GroupKey       string   `json:"-"`
	Logo           string   `json:"logo,omitempty"` // signed local image URL
}

// Query selects a page of the catalog.
type Query struct {
	Kind           string // live, movie or series (required)
	Text           string // full-text search; ignores Sort
	SubscriptionID int64
	CategoryID     string
	// CategorySubscription is the subscription of CategoryID (two
	// providers, or two local collections, use the same ids); 0 any.
	CategorySubscription int64
	Sort                 string // "title" (default) or "recent"
	Section              string // live only: tv, sport, radio
	// All lists every available variant, including what the user's
	// selection hides (for the selection editor).
	All       bool
	NoAdult   bool    // kids profiles
	MinRating float64 // 0: any; with a minimum, titles without a rating are left out
	Cursor    string
	Limit     int
}

// Page is a slice of results with an opaque cursor for the next one.
type Page struct {
	Items []CatalogItem `json:"items"`
	Next  string        `json:"next,omitempty"`
}

type cursor struct {
	Title  string  `json:"t,omitempty"`
	Time   int64   `json:"s,omitempty"`
	Rating float64 `json:"r,omitempty"`
	ID     int64   `json:"i"`
	Off    int     `json:"o,omitempty"`
}

const itemColumns = `i.id, i.subscription_id, i.kind, i.source_id, i.title, i.display_title, i.year, i.category_id,
	i.image, i.epg_id, i.number, i.rating, i.tmdb_id, i.extension, i.catchup_days, i.first_seen_at, i.available,
	i.quality, i.section, i.visible, i.variant_count, i.group_key`

func scanItem(rows *sql.Rows) (CatalogItem, error) {
	var it CatalogItem
	var year, number sql.NullInt64
	var rating sql.NullFloat64
	err := rows.Scan(&it.ID, &it.SubscriptionID, &it.Kind, &it.SourceID, &it.Title, &it.DisplayTitle, &year,
		&it.CategoryID, &it.Image, &it.EPGID, &number, &rating, &it.TMDBID, &it.Extension, &it.CatchupDays,
		&it.FirstSeenAt, &it.Available, &it.Quality, &it.Section, &it.Visible, &it.VariantCount, &it.GroupKey)
	if year.Valid {
		y := int(year.Int64)
		it.Year = &y
	}
	if number.Valid {
		n := int(number.Int64)
		it.Number = &n
	}
	if rating.Valid {
		it.Rating = &rating.Float64
	}
	it.FirstSeenAt /= int64(time.Second) // stored in ns to identify imports
	return it, err
}

// Catalog returns one page. Pagination is keyset-based, so deep pages cost
// the same as the first one.
func (s *Store) Catalog(ctx context.Context, q Query) (Page, error) {
	if q.Kind != "live" && q.Kind != "movie" && q.Kind != "series" {
		return Page{}, errors.New("tipo di catalogo non valido")
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	var cur cursor
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil || json.Unmarshal(raw, &cur) != nil {
			return Page{}, errors.New("cursore non valido")
		}
	}
	where := []string{"i.kind = ?", "i.available = 1"}
	if !q.All {
		where = append(where, "i.visible = 1", "i.primary_variant = 1")
	}
	args := []any{q.Kind}
	if q.Section != "" {
		where, args = append(where, "i.section = ?"), append(args, q.Section)
	}
	if q.NoAdult {
		where = append(where, "i.adult = 0")
	}
	if q.SubscriptionID != 0 {
		where, args = append(where, "i.subscription_id = ?"), append(args, q.SubscriptionID)
	}
	if q.CategoryID != "" {
		// A title is in a category if any of its versions is.
		// One indexed lookup per title (item_variants: kind, group_key).
		sub := q.CategorySubscription
		where = append(where, `((i.category_id = ? AND (? = 0 OR i.subscription_id = ?)) OR (i.group_key <> '' AND EXISTS
			(SELECT 1 FROM item v WHERE v.kind = ? AND v.group_key = i.group_key AND v.category_id = ? AND (? = 0 OR v.subscription_id = ?)
				AND v.available = 1)))`)
		args = append(args, q.CategoryID, sub, sub, q.Kind, q.CategoryID, sub, sub)
	}
	if q.MinRating > 0 {
		where, args = append(where, "i.rating >= ?"), append(args, q.MinRating)
	}

	var sqlText string
	switch {
	case strings.TrimSpace(q.Text) != "":
		match := ftsQuery(q.Text)
		if match == "" {
			return Page{}, nil
		}
		// Relevance order; offset pagination is fine for bounded result sets.
		sqlText = `SELECT ` + itemColumns + ` FROM item_search JOIN item i ON i.id = item_search.rowid
			WHERE item_search MATCH ? AND ` + strings.Join(where, " AND ") + `
			ORDER BY item_search.rank, i.id LIMIT ? OFFSET ?`
		args = append([]any{match}, args...)
		args = append(args, q.Limit+1, cur.Off)
	case q.Sort == "rating":
		if q.Cursor != "" {
			where = append(where, "(coalesce(i.rating, 0) < ? OR (coalesce(i.rating, 0) = ? AND i.id > ?))")
			args = append(args, cur.Rating, cur.Rating, cur.ID)
		}
		sqlText = `SELECT ` + itemColumns + ` FROM item i WHERE ` + strings.Join(where, " AND ") +
			` ORDER BY coalesce(i.rating, 0) DESC, i.id LIMIT ?`
		args = append(args, q.Limit+1)
	case q.Sort == "recent":
		if q.Cursor != "" {
			where = append(where, "(i.first_seen_at < ? OR (i.first_seen_at = ? AND i.id > ?))")
			args = append(args, cur.Time, cur.Time, cur.ID)
		}
		sqlText = `SELECT ` + itemColumns + ` FROM item i WHERE ` + strings.Join(where, " AND ") +
			` ORDER BY i.first_seen_at DESC, i.id LIMIT ?`
		args = append(args, q.Limit+1)
	default:
		if q.Cursor != "" {
			where = append(where, "(i.display_title COLLATE NOCASE > ? OR (i.display_title COLLATE NOCASE = ? AND i.id > ?))")
			args = append(args, cur.Title, cur.Title, cur.ID)
		}
		sqlText = `SELECT ` + itemColumns + ` FROM item i WHERE ` + strings.Join(where, " AND ") +
			` ORDER BY i.display_title COLLATE NOCASE, i.id LIMIT ?`
		args = append(args, q.Limit+1)
	}

	rows, err := s.r.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var page Page
	var lastFirstSeen int64
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return Page{}, err
		}
		page.Items = append(page.Items, it)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[q.Limit-1]
		next := cursor{ID: last.ID}
		switch {
		case strings.TrimSpace(q.Text) != "":
			next = cursor{Off: cur.Off + q.Limit}
		case q.Sort == "rating":
			if err := s.r.QueryRowContext(ctx, `SELECT coalesce(rating, 0) FROM item WHERE id = ?`, last.ID).Scan(&next.Rating); err != nil {
				return Page{}, err
			}
		case q.Sort == "recent":
			// Re-read the exact stored value for the keyset comparison.
			if err := s.r.QueryRowContext(ctx, `SELECT first_seen_at FROM item WHERE id = ?`, last.ID).Scan(&lastFirstSeen); err != nil {
				return Page{}, err
			}
			next.Time = lastFirstSeen
		default:
			next.Title = last.DisplayTitle
		}
		raw, _ := json.Marshal(next)
		page.Next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

// ftsQuery turns user text into a prefix query: every word must match.
func ftsQuery(text string) string {
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(words) > 12 {
		words = words[:12]
	}
	parts := make([]string, 0, len(words))
	for _, w := range words {
		parts = append(parts, `"`+w+`"*`)
	}
	return strings.Join(parts, " AND ")
}

// Counts returns, per kind, the titles shown after the user's selection
// (one per group of variants).
func (s *Store) Counts(ctx context.Context, subID int64) (map[string]int, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT kind, count(*) FROM item WHERE subscription_id = ? AND available = 1
		AND visible = 1 AND primary_variant = 1 GROUP BY kind`, subID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// PlaybackItem is what is needed to open a stream.
type PlaybackItem struct {
	ID             int64
	SubscriptionID int64
	Kind           string
	SourceID       string
	Extension      string
	Locator        []byte
	Title          string
}

func (s *Store) PlaybackItem(ctx context.Context, id int64) (PlaybackItem, error) {
	var it PlaybackItem
	err := s.r.QueryRowContext(ctx, `SELECT id, subscription_id, kind, source_id, extension, locator, display_title
		FROM item WHERE id = ? AND available = 1`, id).
		Scan(&it.ID, &it.SubscriptionID, &it.Kind, &it.SourceID, &it.Extension, &it.Locator, &it.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

// BrowseCategory is a category with how many titles it shows.
type BrowseCategory struct {
	ID           string `json:"id"`
	Subscription int64  `json:"subscription"` // category ids repeat across subscriptions
	Name         string `json:"name"`
	Titles       int    `json:"titles"`
}

// BrowseCategories lists the categories that have visible titles (a title
// counts once, in every category one of its versions is in).
func (s *Store) BrowseCategories(ctx context.Context, kind string, noAdult bool) ([]BrowseCategory, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT c.source_id, c.subscription_id, c.name, count(DISTINCT CASE WHEN v.group_key <> '' THEN v.group_key ELSE CAST(v.id AS TEXT) END)
		FROM item v JOIN category c ON c.subscription_id = v.subscription_id AND c.kind = v.kind AND c.source_id = v.category_id
		WHERE v.kind = ? AND v.available = 1 AND v.visible = 1 AND (? = 0 OR v.adult = 0)
		GROUP BY c.subscription_id, c.source_id ORDER BY c.name COLLATE NOCASE, c.subscription_id`, kind, noAdult)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrowseCategory{}
	for rows.Next() {
		var c BrowseCategory
		if err := rows.Scan(&c.ID, &c.Subscription, &c.Name, &c.Titles); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ItemIDs maps the source ids of a subscription's items of one kind to
// their ids (available or not).
func (s *Store) ItemIDs(ctx context.Context, subID int64, kind string) (map[string]int64, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT source_id, id FROM item WHERE subscription_id = ? AND kind = ?`, subID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var src string
		var id int64
		if err := rows.Scan(&src, &id); err != nil {
			return nil, err
		}
		out[src] = id
	}
	return out, rows.Err()
}

// MyList is a profile's list of films and series ("My list"), the last
// added first: one entry per title, its main version.
func (s *Store) MyList(ctx context.Context, profileID int64, noAdult bool) ([]CatalogItem, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+itemColumns+` FROM favorite f
		JOIN item i ON i.kind = f.kind AND i.group_key = f.group_key AND f.group_key <> ''
		WHERE f.profile_id = ? AND f.kind IN ('movie', 'series') AND i.available = 1 AND i.visible = 1 AND i.primary_variant = 1
			AND (? = 0 OR i.adult = 0)
		ORDER BY f.added_at DESC, f.rowid DESC`, profileID, noAdult)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogItem{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// InMyList tells whether the title of an item is in a profile's list.
func (s *Store) InMyList(ctx context.Context, profileID, itemID int64) (bool, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT count(*) FROM favorite f JOIN item i ON i.kind = f.kind AND i.group_key = f.group_key
		WHERE f.profile_id = ? AND i.id = ? AND f.group_key <> ''`, profileID, itemID).Scan(&n)
	return n > 0, err
}

// TitlesByTMDB finds the visible titles of a kind with these TMDB ids or
// group keys: tmdb id → item, group key → item.
func (s *Store) TitlesByTMDB(ctx context.Context, kind string, ids, keys []string, noAdult bool) (map[string]CatalogItem, map[string]CatalogItem, error) {
	byID, byKey := map[string]CatalogItem{}, map[string]CatalogItem{}
	if len(ids)+len(keys) == 0 {
		return byID, byKey, nil
	}
	args := []any{kind, noAdult}
	in := func(list []string) string {
		if len(list) == 0 {
			return "''"
		}
		for _, v := range list {
			args = append(args, v)
		}
		return "?" + strings.Repeat(", ?", len(list)-1)
	}
	q := `SELECT ` + itemColumns + ` FROM item i WHERE i.kind = ? AND i.available = 1 AND i.visible = 1 AND i.primary_variant = 1
		AND (? = 0 OR i.adult = 0) AND (i.tmdb_id IN (` + in(ids) + `) OR i.group_key IN (` + in(keys) + `))`
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, nil, err
		}
		if it.TMDBID != "" {
			byID[it.TMDBID] = it
		}
		byKey[it.GroupKey] = it
	}
	return byID, byKey, rows.Err()
}
