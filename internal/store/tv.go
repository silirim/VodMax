package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GuideChannel and GuideProgramme are what ReplaceGuide stores.
type GuideChannel struct {
	ID      string
	NameKey string
}

type GuideProgramme struct {
	Channel     string
	Start, Stop int64
	Title       string
	Subtitle    string
	Description string
	Category    string
	Airing      string
}

// guideIDExpr resolves the guide channel of item i: by XMLTV id or by
// name, preferring a channel that has programmes (an external source can
// fill in for a provider channel without guide).
const guideIDExpr = `coalesce((SELECT c.id FROM epg_channel c
	WHERE c.id = lower(i.epg_id) OR (c.name_key = i.name_key AND i.name_key <> '')
	ORDER BY EXISTS (SELECT 1 FROM epg_programme p WHERE p.channel = c.id) DESC, c.id = lower(i.epg_id) DESC
	LIMIT 1), '')`

// ErrGuideEmpty refuses a guide without programmes for a source that
// still has some to come: an overloaded panel answers 200 with an empty
// body, a busy page or <tv></tv>, and taking it would blank the grid until
// the next sync. Once the old programmes are over an empty guide is taken.
var ErrGuideEmpty = errors.New("guida vuota: mantenuta la precedente")

// ReplaceGuide makes channels and programmes the guide of a source
// ("sub:<id>" or "ext:<id>"). Programmes of its channels are replaced as a
// whole.
func (s *Store) ReplaceGuide(ctx context.Context, source string, channels []GuideChannel, programmes []GuideProgramme) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Programmes already over stay (catch-up needs them; a new guide
	// usually starts today); everything else is replaced.
	now := time.Now().Unix()
	if len(programmes) == 0 {
		var upcoming bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM epg_programme WHERE source = ? AND stop >= ?)`, source, now).Scan(&upcoming); err != nil {
			return err
		}
		if upcoming {
			return ErrGuideEmpty
		}
	}
	if err := deleteGuide(ctx, tx, source, now-3600); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM epg_programme WHERE stop < ?`, now-int64(GuideHistory/time.Second)); err != nil {
		return err
	}
	for _, c := range channels {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO epg_channel (source, id, name_key) VALUES (?, ?, ?)`, source, c.ID, c.NameKey); err != nil {
			return err
		}
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO epg_programme (channel, start, stop, title, subtitle, description, category, airing, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range programmes {
		if _, err := stmt.ExecContext(ctx, p.Channel, p.Start, p.Stop, p.Title, p.Subtitle, p.Description, p.Category, p.Airing, source); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Programme is a guide entry as returned to clients.
type Programme struct {
	Start       int64  `json:"start"`
	Stop        int64  `json:"stop"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Airing      string `json:"airing,omitempty"` // live, rerun, premiere, new: from the guide
}

// TVChannel is one entry of the channel list: the primary variant of a
// channel, its number and what is on.
type TVChannel struct {
	ID           int64      `json:"id"`
	Number       int        `json:"number"`
	Official     bool       `json:"official"` // number from the national numbering
	Name         string     `json:"name"`
	Image        string     `json:"image,omitempty"` // provider URL: cleared before reaching clients
	Logo         string     `json:"logo,omitempty"`  // signed local URL
	Section      string     `json:"section"`
	Quality      int        `json:"quality"`
	VariantCount int        `json:"variant_count"`
	Favorite     bool       `json:"favorite"`
	CatchupDays  int        `json:"catchup_days,omitempty"`
	Now          *Programme `json:"now,omitempty"`
	Next         *Programme `json:"next,omitempty"`

	GroupKey string `json:"-"`
	GuideID  string `json:"-"`
}

// FirstFreeNumber is where channels without an official number start.
const FirstFreeNumber = 100

// TVChannels lists channels in remote-control order: officially numbered
// channels first, then the others (TV, sport, radio, each by name) numbered
// from FirstFreeNumber. Numbers are computed over all channels, so a
// channel keeps its number in every section and in the favourites.
func (s *Store) TVChannels(ctx context.Context, profileID int64, noAdult bool, section string, favoritesOnly bool, now time.Time) ([]TVChannel, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT i.id, coalesce(i.lcn, 0), i.display_title, i.image, i.section, i.quality, i.variant_count,
			(SELECT coalesce(max(v.catchup_days), 0) FROM item v WHERE v.group_key = i.group_key AND v.kind = 'live' AND v.available = 1),
			i.group_key, f.position IS NOT NULL, coalesce(f.position, 0),
			`+guideIDExpr+`
		FROM item i
		LEFT JOIN favorite f ON f.profile_id = ? AND f.kind = 'live' AND f.group_key = i.group_key
		WHERE i.kind = 'live' AND i.available = 1 AND i.visible = 1 AND i.primary_variant = 1 AND (? = 0 OR i.adult = 0)
		ORDER BY i.lcn IS NULL, i.lcn,
			CASE i.section WHEN 'tv' THEN 0 WHEN 'sport' THEN 1 ELSE 2 END,
			i.display_title COLLATE NOCASE, i.id`, profileID, noAdult)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []TVChannel
	positions := map[int64]int{}
	next := FirstFreeNumber
	used := map[int]bool{}
	for rows.Next() {
		var c TVChannel
		var pos int
		if err := rows.Scan(&c.ID, &c.Number, &c.Name, &c.Image, &c.Section, &c.Quality, &c.VariantCount, &c.CatchupDays,
			&c.GroupKey, &c.Favorite, &pos, &c.GuideID); err != nil {
			return nil, err
		}
		// Two variants groups can match the same official number (e.g.
		// "Rai Sport" and "Rai Sport +"): the first keeps it.
		if c.Number > 0 && !used[c.Number] {
			c.Official = true
			used[c.Number] = true
		} else {
			c.Number = 0
		}
		if c.Number == 0 {
			for used[next] {
				next++
			}
			c.Number = next
			used[next] = true
		}
		positions[c.ID] = pos
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []TVChannel
	for _, c := range all {
		if (section == "" || c.Section == section) && (!favoritesOnly || c.Favorite) {
			out = append(out, c)
		}
	}
	if favoritesOnly {
		sortByPosition(out, positions)
	}
	return out, s.attachNowNext(ctx, out, now)
}

func sortByPosition(list []TVChannel, pos map[int64]int) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && pos[list[j].ID] < pos[list[j-1].ID]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func (s *Store) attachNowNext(ctx context.Context, list []TVChannel, now time.Time) error {
	byGuide := map[string][]int{}
	for i, c := range list {
		if c.GuideID != "" {
			byGuide[c.GuideID] = append(byGuide[c.GuideID], i)
		}
	}
	if len(byGuide) == 0 {
		return nil
	}
	keys := make([]any, 0, len(byGuide)+2)
	keys = append(keys, now.Unix(), now.Add(12*time.Hour).Unix())
	for k := range byGuide {
		keys = append(keys, k)
	}
	rows, err := s.r.QueryContext(ctx, `SELECT channel, start, stop, title, subtitle, category FROM epg_programme
		WHERE stop > ? AND start < ? AND channel IN (?`+strings.Repeat(",?", len(byGuide)-1)+`)
		ORDER BY channel, start`, keys...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ch string
		var p Programme
		if err := rows.Scan(&ch, &p.Start, &p.Stop, &p.Title, &p.Subtitle, &p.Category); err != nil {
			return err
		}
		for _, i := range byGuide[ch] {
			c := &list[i]
			switch {
			case c.Now == nil && p.Start <= now.Unix():
				pc := p
				c.Now = &pc
			case c.Next == nil && p.Start > now.Unix():
				pc := p
				c.Next = &pc
			}
		}
	}
	return rows.Err()
}

// ChannelGuide returns the programmes of a channel between from and to.
func (s *Store) ChannelGuide(ctx context.Context, itemID int64, from, to time.Time) ([]Programme, error) {
	var guide string
	err := s.r.QueryRowContext(ctx, `SELECT
			`+guideIDExpr+`
		FROM item i WHERE i.id = ? AND i.kind = 'live'`, itemID).Scan(&guide)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil || guide == "" {
		return []Programme{}, err
	}
	rows, err := s.r.QueryContext(ctx, `SELECT start, stop, title, subtitle, description, category FROM epg_programme
		WHERE channel = ? AND stop > ? AND start < ? ORDER BY start`, guide, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Programme{}
	for rows.Next() {
		var p Programme
		if err := rows.Scan(&p.Start, &p.Stop, &p.Title, &p.Subtitle, &p.Description, &p.Category); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GuideChannelKeys lists the guide channel ids and name keys of the
// visible channels, so the guide import keeps only what can be shown.
func (s *Store) GuideChannelKeys(ctx context.Context) (ids, names map[string]bool, err error) {
	rows, err := s.r.QueryContext(ctx, `SELECT lower(epg_id), name_key FROM item WHERE kind = 'live' AND available = 1 AND visible = 1`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	ids, names = map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, err
		}
		if id != "" {
			ids[id] = true
		}
		if name != "" {
			names[name] = true
		}
	}
	return ids, names, rows.Err()
}

// SetFavorite adds or removes a channel (by its group of variants) from a
// profile's favourites; new favourites go to the end.
func (s *Store) SetFavorite(ctx context.Context, profileID, itemID int64, favorite bool) error {
	var kind, group string
	err := s.r.QueryRowContext(ctx, `SELECT kind, group_key FROM item WHERE id = ?`, itemID).Scan(&kind, &group)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !favorite {
		_, err = s.w.ExecContext(ctx, `DELETE FROM favorite WHERE profile_id = ? AND kind = ? AND group_key = ?`, profileID, kind, group)
		return err
	}
	_, err = s.w.ExecContext(ctx, `INSERT OR IGNORE INTO favorite VALUES (?, ?, ?,
		(SELECT coalesce(max(position), 0) + 1 FROM favorite WHERE profile_id = ? AND kind = ?), ?)`,
		profileID, kind, group, profileID, kind, time.Now().Unix())
	return err
}

// CachedImage is an image fetched from a provider. An empty ContentType
// records a failed fetch, so it is not retried at every request.
type CachedImage struct {
	ContentType string
	Data        []byte
	FetchedAt   time.Time
}

func (s *Store) CachedImage(ctx context.Context, urlHash string) (CachedImage, bool, error) {
	var img CachedImage
	var at int64
	err := s.r.QueryRowContext(ctx, `SELECT content_type, data, fetched_at FROM image_cache WHERE url_hash = ?`, urlHash).
		Scan(&img.ContentType, &img.Data, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return img, false, nil
	}
	img.FetchedAt = time.Unix(at, 0)
	return img, err == nil, err
}

func (s *Store) PutCachedImage(ctx context.Context, urlHash string, img CachedImage) error {
	_, err := s.w.ExecContext(ctx, `INSERT OR REPLACE INTO image_cache VALUES (?, ?, ?, ?)`,
		urlHash, img.ContentType, img.Data, time.Now().Unix())
	return err
}

// PruneImageCache drops the images fetched before old, then the oldest
// until the rest weighs at most budget bytes. The cache refills itself
// as images are asked again.
func (s *Store) PruneImageCache(ctx context.Context, old time.Time, budget int64) (int64, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM image_cache WHERE fetched_at < ?`, old.Unix())
	if err != nil {
		return 0, err
	}
	aged, _ := res.RowsAffected()
	res, err = s.w.ExecContext(ctx, `DELETE FROM image_cache WHERE url_hash IN (
		SELECT url_hash FROM (SELECT url_hash, SUM(length(data)) OVER (ORDER BY fetched_at DESC, url_hash) AS kept FROM image_cache)
		WHERE kept > ?)`, budget)
	if err != nil {
		return aged, err
	}
	over, _ := res.RowsAffected()
	return aged + over, nil
}

// ItemImage returns the provider image URL and subscription of an item.
func (s *Store) ItemImage(ctx context.Context, itemID int64) (string, int64, error) {
	var url string
	var sub int64
	err := s.r.QueryRowContext(ctx, `SELECT image, subscription_id FROM item WHERE id = ?`, itemID).Scan(&url, &sub)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	return url, sub, err
}

// GuideWindow returns the programmes of the given guide channels that
// overlap [from, to), by guide channel.
func (s *Store) GuideWindow(ctx context.Context, guides []string, from, to time.Time) (map[string][]Programme, error) {
	out := map[string][]Programme{}
	if len(guides) == 0 {
		return out, nil
	}
	// One query per batch: SQLite limits bound parameters.
	for len(guides) > 0 {
		n := min(len(guides), 400)
		batch := guides[:n]
		guides = guides[n:]
		args := []any{from.Unix(), to.Unix()}
		for _, g := range batch {
			args = append(args, g)
		}
		rows, err := s.r.QueryContext(ctx, `SELECT channel, start, stop, title, subtitle, description, category, airing FROM epg_programme
			WHERE stop > ? AND start < ? AND channel IN (?`+strings.Repeat(", ?", n-1)+`) ORDER BY channel, start`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ch string
			var p Programme
			if err := rows.Scan(&ch, &p.Start, &p.Stop, &p.Title, &p.Subtitle, &p.Description, &p.Category, &p.Airing); err != nil {
				rows.Close()
				return nil, err
			}
			out[ch] = append(out[ch], p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GuideHistory is how long programmes are kept after they end (archives
// of the providers go back a week).
const GuideHistory = 7 * 24 * time.Hour

// deleteGuide removes a source's channels and programmes: the programmes
// it added (also to other sources' channels) and those of its channels,
// unless another source has a channel with the same id. Programmes that
// ended before keepBefore stay (0: none).
func deleteGuide(ctx context.Context, tx *sql.Tx, source string, keepBefore int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM epg_programme WHERE stop >= ? AND (source = ?
		OR (channel IN (SELECT id FROM epg_channel WHERE source = ?) AND channel NOT IN (SELECT id FROM epg_channel WHERE source <> ?)))`,
		keepBefore, source, source, source); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM epg_channel WHERE source = ?`, source)
	return err
}

// DeleteGuide removes the guide of a source.
func (s *Store) DeleteGuide(ctx context.Context, source string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteGuide(ctx, tx, source, 0); err != nil {
		return err
	}
	return tx.Commit()
}

// GuideCover is what the guide has for a visible channel: its guide
// channel (if any) and the end of its last programme (0 if none ahead).
type GuideCover struct {
	EPGID, NameKey, Guide string
	LastStop              int64
}

// GuideCoverage describes every visible channel, and lists the guide
// channel ids that sources other than except use.
func (s *Store) GuideCoverage(ctx context.Context, except string, now time.Time) ([]GuideCover, map[string]bool, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT lower(i.epg_id), i.name_key, `+guideIDExpr+` AS g
		FROM item i WHERE i.kind = 'live' AND i.available = 1 AND i.visible = 1 AND i.primary_variant = 1`)
	if err != nil {
		return nil, nil, err
	}
	var list []GuideCover
	for rows.Next() {
		var c GuideCover
		if err := rows.Scan(&c.EPGID, &c.NameKey, &c.Guide); err != nil {
			rows.Close()
			return nil, nil, err
		}
		list = append(list, c)
	}
	rows.Close()
	for i := range list {
		if list[i].Guide == "" {
			continue
		}
		// Only programmes of other sources count: this source's own
		// extensions are replaced.
		if err := s.r.QueryRowContext(ctx, `SELECT coalesce(max(stop), 0) FROM epg_programme WHERE channel = ? AND stop > ? AND source <> ?`,
			list[i].Guide, now.Unix(), except).Scan(&list[i].LastStop); err != nil {
			return nil, nil, err
		}
	}
	taken := map[string]bool{}
	rows, err = s.r.QueryContext(ctx, `SELECT DISTINCT id FROM epg_channel WHERE source <> ?`, except)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, nil, err
		}
		taken[id] = true
	}
	return list, taken, rows.Err()
}

// GuideSource is an external XMLTV guide.
type GuideSource struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Enabled    bool   `json:"enabled"`
	SyncedAt   *int64 `json:"synced_at,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	Channels   int    `json:"channels"`
	Programmes int    `json:"programmes"`
}

const guideSourceColumns = `id, name, url, enabled, synced_at, status, error, channels, programmes`

func scanGuideSource(row interface{ Scan(...any) error }) (GuideSource, error) {
	var g GuideSource
	var synced sql.NullInt64
	err := row.Scan(&g.ID, &g.Name, &g.URL, &g.Enabled, &synced, &g.Status, &g.Error, &g.Channels, &g.Programmes)
	if synced.Valid {
		g.SyncedAt = &synced.Int64
	}
	return g, err
}

func (s *Store) GuideSources(ctx context.Context) ([]GuideSource, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+guideSourceColumns+` FROM guide_source ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GuideSource{}
	for rows.Next() {
		g, err := scanGuideSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) GuideSource(ctx context.Context, id int64) (GuideSource, error) {
	g, err := scanGuideSource(s.r.QueryRowContext(ctx, `SELECT `+guideSourceColumns+` FROM guide_source WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	return g, err
}

func (s *Store) AddGuideSource(ctx context.Context, name, url string) (int64, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO guide_source (name, url, created_at) VALUES (?, ?, ?)`, name, url, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateGuideSource(ctx context.Context, g GuideSource) error {
	res, err := s.w.ExecContext(ctx, `UPDATE guide_source SET name = ?, url = ?, enabled = ? WHERE id = ?`, g.Name, g.URL, g.Enabled, g.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetGuideSourceResult(ctx context.Context, id int64, status, errText string, channels, programmes int) error {
	_, err := s.w.ExecContext(ctx, `UPDATE guide_source SET synced_at = ?, status = ?, error = ?,
		channels = CASE WHEN ? = 'ok' THEN ? ELSE channels END, programmes = CASE WHEN ? = 'ok' THEN ? ELSE programmes END WHERE id = ?`,
		time.Now().Unix(), status, errText, status, channels, status, programmes, id)
	return err
}

func (s *Store) DeleteGuideSource(ctx context.Context, id int64) error {
	if err := s.DeleteGuide(ctx, fmt.Sprintf("ext:%d", id)); err != nil {
		return err
	}
	res, err := s.w.ExecContext(ctx, `DELETE FROM guide_source WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
