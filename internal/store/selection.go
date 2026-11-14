package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// SelectionCategory is a category with the user's overrides, used to
// compute what is shown.
type SelectionCategory struct {
	Category
	SubscriptionID int64 `json:"subscription_id"`
	Items          int   `json:"items"` // available items
	// Overrides: nil means "decided by the rules".
	Enabled         *bool   `json:"enabled_override,omitempty"`
	SectionOverride *string `json:"section_override,omitempty"`
	CountryOverride *string `json:"country_override,omitempty"`
	AdultOverride   *bool   `json:"adult_override,omitempty"`
}

// SelectionItem carries what the selection needs about an item, and the
// result to store.
type SelectionItem struct {
	ID             int64
	SubscriptionID int64
	Kind           string
	CategoryID     string
	GroupKey       string
	Year           int
	Quality        int
	Hidden         bool // per-item user override

	Visible      bool
	Primary      bool
	VariantCount int
	Section      string
	Adult        bool // from the category: never shown to kids profiles
}

// SelectionCategories loads all categories with overrides and item counts.
func (s *Store) SelectionCategories(ctx context.Context, subID int64) ([]SelectionCategory, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT c.subscription_id, c.kind, c.source_id, c.name, c.country, c.adult, c.low_res, c.section,
			o.enabled, o.section, o.country, o.adult,
			(SELECT count(*) FROM item i WHERE i.subscription_id = c.subscription_id AND i.kind = c.kind
				AND i.category_id = c.source_id AND i.available = 1)
		FROM category c LEFT JOIN category_override o
			ON o.subscription_id = c.subscription_id AND o.kind = c.kind AND o.source_id = c.source_id
		WHERE ? = 0 OR c.subscription_id = ?
		ORDER BY c.subscription_id, c.kind, c.name COLLATE NOCASE`, subID, subID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SelectionCategory
	for rows.Next() {
		var c SelectionCategory
		var enabled, adult sql.NullBool
		var section, country sql.NullString
		if err := rows.Scan(&c.SubscriptionID, &c.Kind, &c.SourceID, &c.Name, &c.Country, &c.Adult, &c.LowRes, &c.Section,
			&enabled, &section, &country, &adult, &c.Items); err != nil {
			return nil, err
		}
		if enabled.Valid {
			c.Enabled = &enabled.Bool
		}
		if adult.Valid {
			c.AdultOverride = &adult.Bool
		}
		if section.Valid {
			c.SectionOverride = &section.String
		}
		if country.Valid {
			c.CountryOverride = &country.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SelectionItems loads every available item with its current selection.
func (s *Store) SelectionItems(ctx context.Context) ([]SelectionItem, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT i.id, i.subscription_id, i.kind, i.category_id, i.group_key, coalesce(i.year, 0), i.quality,
			coalesce(o.hidden, 0), i.visible, i.primary_variant, i.variant_count, i.section, i.adult
		FROM item i LEFT JOIN item_override o ON o.item_id = i.id
		WHERE i.available = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SelectionItem
	for rows.Next() {
		var it SelectionItem
		if err := rows.Scan(&it.ID, &it.SubscriptionID, &it.Kind, &it.CategoryID, &it.GroupKey, &it.Year, &it.Quality,
			&it.Hidden, &it.Visible, &it.Primary, &it.VariantCount, &it.Section, &it.Adult); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// WriteSelection stores the computed selection for the given items.
// relabel holds items whose visibility and primary flag stay the same:
// only their other fields are written, which leaves the three browse
// indexes alone (most of the cost of a selection over a large catalog).
func (s *Store) WriteSelection(ctx context.Context, items, relabel []SelectionItem) error {
	if len(items)+len(relabel) == 0 {
		return nil // no transaction, and no cache emptied for nothing
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := coldCache(ctx, tx); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE item SET visible = ?, primary_variant = ?, variant_count = ?, section = ?, adult = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, it := range items {
		if _, err := stmt.ExecContext(ctx, it.Visible, it.Primary, it.VariantCount, it.Section, it.Adult, it.ID); err != nil {
			return err
		}
	}
	labels, err := tx.PrepareContext(ctx, `UPDATE item SET variant_count = ?, section = ?, adult = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer labels.Close()
	for _, it := range relabel {
		if _, err := labels.ExecContext(ctx, it.VariantCount, it.Section, it.Adult, it.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	coldCache(ctx, s.w)
	return nil
}

// Rules returns the raw JSON selection rules of a subscription.
func (s *Store) Rules(ctx context.Context, subID int64) (json.RawMessage, error) {
	var raw string
	err := s.r.QueryRowContext(ctx, `SELECT rules FROM subscription WHERE id = ?`, subID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return json.RawMessage(raw), err
}

// AllRules returns the rules of every subscription.
func (s *Store) AllRules(ctx context.Context) (map[int64]json.RawMessage, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, rules FROM subscription`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]json.RawMessage{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		out[id] = json.RawMessage(raw)
	}
	return out, rows.Err()
}

func (s *Store) SetRules(ctx context.Context, subID int64, rules json.RawMessage) error {
	res, err := s.w.ExecContext(ctx, `UPDATE subscription SET rules = ? WHERE id = ?`, string(rules), subID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CategoryOverride is a user correction of one category; nil fields reset
// to the automatic value.
type CategoryOverride struct {
	Enabled *bool   `json:"enabled"`
	Section *string `json:"section"`
	Country *string `json:"country"`
	Adult   *bool   `json:"adult"`
}

func (s *Store) SetCategoryOverride(ctx context.Context, subID int64, kind, sourceID string, o CategoryOverride) error {
	if o.Enabled == nil && o.Section == nil && o.Country == nil && o.Adult == nil {
		_, err := s.w.ExecContext(ctx, `DELETE FROM category_override WHERE subscription_id = ? AND kind = ? AND source_id = ?`,
			subID, kind, sourceID)
		return err
	}
	_, err := s.w.ExecContext(ctx, `INSERT INTO category_override VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (subscription_id, kind, source_id) DO UPDATE SET
			enabled = excluded.enabled, section = excluded.section, country = excluded.country, adult = excluded.adult`,
		subID, kind, sourceID, o.Enabled, o.Section, o.Country, o.Adult)
	return err
}

// HasCategoryOverrides tells whether a subscription has category choices,
// even before a sync brings its categories (after a restore).
func (s *Store) HasCategoryOverrides(ctx context.Context, subID int64) (bool, error) {
	var found bool
	err := s.r.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM category_override WHERE subscription_id = ?)`, subID).Scan(&found)
	return found, err
}

// SetItemHidden hides or shows an item with all its versions (the same
// channel or title in other qualities or subscriptions): hiding one alone
// would leave the card on screen with the next version.
func (s *Store) SetItemHidden(ctx context.Context, itemID int64, hidden bool) error {
	const group = `SELECT v.id FROM item i JOIN item v ON v.kind = i.kind
		AND (v.id = i.id OR (i.group_key != '' AND v.group_key = i.group_key)) WHERE i.id = ?`
	var err error
	if hidden {
		_, err = s.w.ExecContext(ctx, `INSERT OR REPLACE INTO item_override (item_id, hidden) SELECT id, 1 FROM (`+group+`)`, itemID)
	} else {
		_, err = s.w.ExecContext(ctx, `DELETE FROM item_override WHERE item_id IN (`+group+`)`, itemID)
	}
	return err
}

// HiddenItem is a channel or title hidden by hand.
type HiddenItem struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// HiddenItems lists what was hidden by hand in a subscription, one row
// per channel or title (not per version), by name.
func (s *Store) HiddenItems(ctx context.Context, subID int64, kind string) ([]HiddenItem, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT min(i.id), i.display_title FROM item_override o JOIN item i ON i.id = o.item_id
		WHERE o.hidden = 1 AND i.subscription_id = ? AND i.kind = ? AND i.available = 1
		GROUP BY CASE WHEN i.group_key = '' THEN 'id:' || i.id ELSE i.group_key END
		ORDER BY i.display_title COLLATE NOCASE`, subID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HiddenItem{}
	for rows.Next() {
		var h HiddenItem
		if err := rows.Scan(&h.ID, &h.Name); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Variants lists the available versions of the same title or channel as
// itemID, best quality first.
func (s *Store) Variants(ctx context.Context, itemID int64) ([]CatalogItem, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+itemColumns+` FROM item i
		JOIN item ref ON ref.id = ? AND ref.kind = i.kind AND ref.group_key = i.group_key
		WHERE i.available = 1 AND (i.id = ref.id OR ref.group_key <> '')
		ORDER BY i.visible DESC, i.quality DESC, i.id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatalogItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}
