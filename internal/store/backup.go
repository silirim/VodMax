package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// Backup is the user's data, without the catalog (downloaded again from
// the providers). Items are referenced by subscription, kind and provider
// id, so the backup survives a new database where row ids differ. Secrets
// are in clear here: the caller encrypts the whole backup.
type Backup struct {
	Version       int                 `json:"version"`
	CreatedAt     time.Time           `json:"created_at"`
	VPNs          []BackupVPN         `json:"vpns"`
	Subscriptions []BackupSub         `json:"subscriptions"`
	Categories    []BackupCategory    `json:"category_overrides"`
	Hidden        []BackupItemRef     `json:"hidden_items"`
	Favorites     []BackupFavorite    `json:"favorites"`
	Progress      []BackupProgress    `json:"progress"`
	Recordings    []Recording         `json:"recordings"`
	RecordingKeys map[int64]string    `json:"recording_groups"` // recording id → channel group
	Settings      map[string]string   `json:"settings"`
	Secrets       map[string]string   `json:"secrets,omitempty"` // sealed settings, in clear inside the backup
	GuideSources  []GuideSource       `json:"guide_sources,omitempty"`
	Rules         []RecordingRule     `json:"recording_rules,omitempty"`
	Extra         map[string][]string `json:"extra,omitempty"` // reserved for later tables
	// Missing in backups made before 0.6.7: profiles are then left as
	// they are, and recordings keep no file.
	Profiles       []BackupProfile  `json:"profiles,omitempty"`
	ContinueHidden []BackupHidden   `json:"continue_hidden,omitempty"`
	RecordingFiles map[int64]string `json:"recording_files,omitempty"` // recording id → file name in the recordings folder
}

// BackupProfile is a profile with its PIN (hashed) and kind (kids).
type BackupProfile struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	PINHash   string `json:"pin_hash,omitempty"`
	Color     string `json:"color,omitempty"`
	Avatar    string `json:"avatar,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// BackupHidden is a title a profile took off "continue watching".
type BackupHidden struct {
	ProfileID int64  `json:"profile_id"`
	TitleKey  string `json:"title_key"`
	HiddenAt  int64  `json:"hidden_at"`
}

const BackupVersion = 1

type BackupVPN struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind,omitempty"`
	Config string `json:"config"`
}

type BackupSub struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	VPNID    int64           `json:"vpn_id"`
	Settings json.RawMessage `json:"settings"`
	Rules    json.RawMessage `json:"rules"`
	Secret   json.RawMessage `json:"secret"`
}

type BackupCategory struct {
	SubscriptionID int64   `json:"subscription_id"`
	Kind           string  `json:"kind"`
	SourceID       string  `json:"source_id"`
	Enabled        *bool   `json:"enabled,omitempty"`
	Section        *string `json:"section,omitempty"`
	Country        *string `json:"country,omitempty"`
	Adult          *bool   `json:"adult,omitempty"`
}

// BackupItemRef names a catalog item independently of its row id.
type BackupItemRef struct {
	SubscriptionID int64  `json:"subscription_id"`
	Kind           string `json:"kind"`
	SourceID       string `json:"source_id"`
}

type BackupFavorite struct {
	ProfileID int64  `json:"profile_id"`
	Kind      string `json:"kind"`
	GroupKey  string `json:"group_key"`
	Position  int    `json:"position"`
	AddedAt   int64  `json:"added_at"`
}

type BackupEpisode struct {
	SourceID  string `json:"source_id"`
	Season    int    `json:"season"`
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Extension string `json:"extension"`
	Duration  int    `json:"duration"`
	Plot      string `json:"plot"`
}

type BackupProgress struct {
	ProfileID int64          `json:"profile_id"`
	Media     string         `json:"media"`
	Item      BackupItemRef  `json:"item"` // the film, or the series of the episode
	Episode   *BackupEpisode `json:"episode,omitempty"`
	Position  int            `json:"position"`
	Duration  int            `json:"duration"`
	Completed bool           `json:"completed"`
	UpdatedAt int64          `json:"updated_at"`
}

// ExportBackup reads the user's data. open decrypts the stored secrets.
func (s *Store) ExportBackup(ctx context.Context, openVPN, openSub func(id int64, sealed []byte) ([]byte, error)) (*Backup, error) {
	tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	b := &Backup{Version: BackupVersion, CreatedAt: time.Now().UTC(), Settings: map[string]string{}, RecordingKeys: map[int64]string{},
		RecordingFiles: map[int64]string{}}

	err = eachRow(ctx, tx, `SELECT id, name, kind, config FROM vpn ORDER BY id`, func(rows *sql.Rows) error {
		var v BackupVPN
		var sealed []byte
		if err := rows.Scan(&v.ID, &v.Name, &v.Kind, &sealed); err != nil {
			return err
		}
		plain, err := openVPN(v.ID, sealed)
		if err != nil {
			return fmt.Errorf("VPN %d: %w", v.ID, err)
		}
		v.Config = string(plain)
		b.VPNs = append(b.VPNs, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT id, name, kind, coalesce(vpn_id, 0), settings, rules, secret FROM subscription ORDER BY id`, func(rows *sql.Rows) error {
		var sub BackupSub
		var settings, rules string
		var sealed []byte
		if err := rows.Scan(&sub.ID, &sub.Name, &sub.Kind, &sub.VPNID, &settings, &rules, &sealed); err != nil {
			return err
		}
		plain, err := openSub(sub.ID, sealed)
		if err != nil {
			return fmt.Errorf("abbonamento %d: %w", sub.ID, err)
		}
		sub.Settings, sub.Rules, sub.Secret = json.RawMessage(settings), json.RawMessage(rules), json.RawMessage(plain)
		b.Subscriptions = append(b.Subscriptions, sub)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT subscription_id, kind, source_id, enabled, section, country, adult FROM category_override`, func(rows *sql.Rows) error {
		var c BackupCategory
		var enabled, adult sql.NullBool
		var section, country sql.NullString
		if err := rows.Scan(&c.SubscriptionID, &c.Kind, &c.SourceID, &enabled, &section, &country, &adult); err != nil {
			return err
		}
		if enabled.Valid {
			c.Enabled = &enabled.Bool
		}
		if adult.Valid {
			c.Adult = &adult.Bool
		}
		if section.Valid {
			c.Section = &section.String
		}
		if country.Valid {
			c.Country = &country.String
		}
		b.Categories = append(b.Categories, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT i.subscription_id, i.kind, i.source_id FROM item_override o JOIN item i ON i.id = o.item_id WHERE o.hidden = 1`, func(rows *sql.Rows) error {
		var r BackupItemRef
		if err := rows.Scan(&r.SubscriptionID, &r.Kind, &r.SourceID); err != nil {
			return err
		}
		b.Hidden = append(b.Hidden, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT profile_id, kind, group_key, position, added_at FROM favorite ORDER BY profile_id, kind, position`, func(rows *sql.Rows) error {
		var f BackupFavorite
		if err := rows.Scan(&f.ProfileID, &f.Kind, &f.GroupKey, &f.Position, &f.AddedAt); err != nil {
			return err
		}
		b.Favorites = append(b.Favorites, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT p.profile_id, p.media, p.position, p.duration, p.completed, p.updated_at,
			i.subscription_id, i.kind, i.source_id,
			e.source_id, e.season, e.number, e.title, e.extension, e.duration, e.plot
		FROM progress p
		LEFT JOIN episode e ON p.media = 'episode' AND e.id = p.media_id
		JOIN item i ON i.id = CASE p.media WHEN 'movie' THEN p.media_id ELSE e.series_item_id END`, func(rows *sql.Rows) error {
		var p BackupProgress
		var eSource, eTitle, eExt, ePlot sql.NullString
		var eSeason, eNumber, eDuration sql.NullInt64
		if err := rows.Scan(&p.ProfileID, &p.Media, &p.Position, &p.Duration, &p.Completed, &p.UpdatedAt,
			&p.Item.SubscriptionID, &p.Item.Kind, &p.Item.SourceID,
			&eSource, &eSeason, &eNumber, &eTitle, &eExt, &eDuration, &ePlot); err != nil {
			return err
		}
		if eSource.Valid {
			p.Episode = &BackupEpisode{SourceID: eSource.String, Season: int(eSeason.Int64), Number: int(eNumber.Int64),
				Title: eTitle.String, Extension: eExt.String, Duration: int(eDuration.Int64), Plot: ePlot.String}
		}
		b.Progress = append(b.Progress, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT `+recordingColumns+` FROM recording ORDER BY id`, func(rows *sql.Rows) error {
		r, err := scanRecording(rows)
		if err != nil {
			return err
		}
		b.Recordings = append(b.Recordings, r)
		b.RecordingKeys[r.ID] = r.GroupKey
		if r.File != "" {
			b.RecordingFiles[r.ID] = filepath.Base(r.File)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT id, name, kind, pin_hash, color, avatar, created_at FROM profile ORDER BY id`, func(rows *sql.Rows) error {
		var p BackupProfile
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.PINHash, &p.Color, &p.Avatar, &p.CreatedAt); err != nil {
			return err
		}
		b.Profiles = append(b.Profiles, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT profile_id, title_key, hidden_at FROM continue_hidden`, func(rows *sql.Rows) error {
		var h BackupHidden
		if err := rows.Scan(&h.ProfileID, &h.TitleKey, &h.HiddenAt); err != nil {
			return err
		}
		b.ContinueHidden = append(b.ContinueHidden, h)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT `+ruleColumns+` FROM recording_rule ORDER BY id`, func(rows *sql.Rows) error {
		r, err := scanRule(rows)
		if err != nil {
			return err
		}
		b.Rules = append(b.Rules, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT name, url, enabled FROM guide_source ORDER BY id`, func(rows *sql.Rows) error {
		var g GuideSource
		if err := rows.Scan(&g.Name, &g.URL, &g.Enabled); err != nil {
			return err
		}
		b.GuideSources = append(b.GuideSources, g)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, tx, `SELECT key, value FROM setting`, func(rows *sql.Rows) error {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		b.Settings[k] = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

func eachRow(ctx context.Context, tx *sql.Tx, query string, fn func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// pendingKey holds the parts of a restored backup that need catalog items
// (hidden items, watch progress): applied after each synchronisation.
const pendingKey = "restore_pending"

type pendingRestore struct {
	Hidden   []BackupItemRef  `json:"hidden"`
	Progress []BackupProgress `json:"progress"`
}

// ImportBackup replaces the user's data with the backup's; the catalog is
// emptied and must be synchronised again. seal encrypts the secrets for
// this server. recFile returns the path of a file of the recordings
// folder by its name, "" if it is not there: a restored recording points
// only there, and the recordings made after the backup whose file is
// still there are kept.
func (s *Store) ImportBackup(ctx context.Context, b *Backup, sealVPN, sealSub func(id int64, plain []byte) []byte,
	recFile func(name string) string) error {
	if b.Version < 1 || b.Version > BackupVersion {
		return fmt.Errorf("backup in un formato non supportato (versione %d)", b.Version)
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current []Recording
	err = eachRow(ctx, tx, `SELECT `+recordingColumns+` FROM recording WHERE status = 'completed' AND file <> ''`, func(rows *sql.Rows) error {
		r, err := scanRecording(rows)
		current = append(current, r)
		return err
	})
	if err != nil {
		return err
	}
	// Downloads stay, but the catalog ids they point to are about to be
	// reused by other titles: they are found again after the next sync.
	if _, err := tx.ExecContext(ctx, `UPDATE download SET media_id = -id WHERE media_id > 0`); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM progress`, `DELETE FROM continue_hidden`, `DELETE FROM favorite`, `DELETE FROM recording`, `DELETE FROM setting`,
		`DELETE FROM epg_programme`, `DELETE FROM epg_channel`, `DELETE FROM guide_source`, `DELETE FROM recording_rule`,
		`DELETE FROM subscription`, `DELETE FROM vpn`, // cascades to the catalog
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	now := time.Now().Unix()
	for _, v := range b.VPNs {
		kind := v.Kind
		if kind == "" {
			kind = "wireguard"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO vpn (id, name, kind, config, created_at) VALUES (?, ?, ?, ?, ?)`,
			v.ID, v.Name, kind, sealVPN(v.ID, []byte(v.Config)), now); err != nil {
			return err
		}
	}
	for _, sub := range b.Subscriptions {
		rules := string(sub.Rules)
		if rules == "" || rules == "null" {
			rules = "{}"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO subscription (id, name, kind, vpn_id, settings, rules, secret, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, sub.ID, sub.Name, sub.Kind, nullID(sub.VPNID), string(sub.Settings), rules,
			sealSub(sub.ID, sub.Secret), now); err != nil {
			return fmt.Errorf("abbonamento %q: %w", sub.Name, err)
		}
	}
	for _, c := range b.Categories {
		if _, err := tx.ExecContext(ctx, `INSERT INTO category_override (subscription_id, kind, source_id, enabled, section, country, adult)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, c.SubscriptionID, c.Kind, c.SourceID, c.Enabled, c.Section, c.Country, c.Adult); err != nil {
			return err
		}
	}
	for _, f := range b.Favorites {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO favorite (profile_id, kind, group_key, position, added_at) VALUES (?, ?, ?, ?, ?)`,
			f.ProfileID, f.Kind, f.GroupKey, f.Position, f.AddedAt); err != nil {
			return err
		}
	}
	restored := map[string]bool{}
	byID := map[int64]Recording{}
	for _, r := range current {
		byID[r.ID] = r
	}
	for _, r := range b.Recordings {
		status := r.Status
		name := filepath.Base(b.RecordingFiles[r.ID])
		c, same := byID[r.ID]
		same = same && c.Start == r.Start && c.GroupKey == b.RecordingKeys[r.ID]
		if same && status == "recording" && recFile(filepath.Base(c.File)) != "" {
			// Running when the copy was made, finished since on this
			// server and its file (MP4 after the conversion, or the TS) is
			// here: the row of now is the truth.
			r, status, name = c, c.Status, filepath.Base(c.File)
		} else if same && b.RecordingFiles == nil {
			name = filepath.Base(c.File) // an older backup, on the same server
		}
		if status == "recording" {
			status, r.Error = "failed", "interrotta (ripristino da backup)"
		}
		r.File = ""
		if name != "." {
			r.File = recFile(name)
			restored[name] = true
		}
		if status == "completed" && r.File == "" {
			status, r.Error = "failed", "file della registrazione non trovato dopo il ripristino"
		}
		var rule any
		if r.RuleID != 0 {
			rule = r.RuleID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO recording (id, item_id, group_key, channel, title, start, stop, status, file, bytes, error, created_at, rule_id, protected)
			VALUES (?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, b.RecordingKeys[r.ID], r.Channel, r.Title, r.Start, r.Stop,
			status, r.File, r.Bytes, r.Error, r.CreatedAt, rule, r.Protected); err != nil {
			return err
		}
	}
	// Recordings made after the backup: their files are still here.
	ruleIDs := map[int64]bool{}
	for _, r := range b.Rules {
		ruleIDs[r.ID] = true
	}
	for _, r := range current {
		name := filepath.Base(r.File)
		if restored[name] || recFile(name) == "" {
			continue
		}
		var rule any
		if ruleIDs[r.RuleID] {
			rule = r.RuleID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO recording (id, item_id, group_key, channel, title, start, stop, status, file, bytes, error, created_at, rule_id, protected)
			VALUES ((SELECT CASE WHEN EXISTS (SELECT 1 FROM recording WHERE id = ?) THEN NULL ELSE ? END), 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.ID, r.GroupKey, r.Channel, r.Title, r.Start, r.Stop, r.Status, recFile(name), r.Bytes, r.Error, r.CreatedAt, rule, r.Protected); err != nil {
			return err
		}
	}
	if len(b.Profiles) > 0 {
		if err := importProfiles(ctx, tx, b.Profiles); err != nil {
			return err
		}
	}
	for _, h := range b.ContinueHidden {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO continue_hidden (profile_id, title_key, hidden_at) VALUES (?, ?, ?)`,
			h.ProfileID, h.TitleKey, h.HiddenAt); err != nil {
			return err
		}
	}
	for _, r := range b.Rules {
		spec, _ := json.Marshal(r.Spec)
		if _, err := tx.ExecContext(ctx, `INSERT INTO recording_rule (id, name, spec, keep, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			r.ID, r.Name, string(spec), r.Keep, r.Enabled, r.CreatedAt); err != nil {
			return err
		}
	}
	for _, g := range b.GuideSources {
		if _, err := tx.ExecContext(ctx, `INSERT INTO guide_source (name, url, enabled, created_at) VALUES (?, ?, ?, ?)`,
			g.Name, g.URL, g.Enabled, now); err != nil {
			return err
		}
	}
	for k, v := range b.Settings {
		if k == pendingKey {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO setting (key, value) VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}
	pending, _ := json.Marshal(pendingRestore{Hidden: b.Hidden, Progress: b.Progress})
	if _, err := tx.ExecContext(ctx, `INSERT INTO setting (key, value) VALUES (?, ?)`, pendingKey, string(pending)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = s.w.ExecContext(ctx, `INSERT INTO item_search(item_search) VALUES ('rebuild')`)
	return err
}

// importProfiles replaces the profiles with the backup's. Profile 1 (the
// first administrator) always stays. Every profile starts a new PIN epoch,
// so no device keeps a profile chosen before the restore. Devices (not in
// backups) locked to a profile follow its name and kind; one whose profile
// is gone is unpaired, never left free or on another profile.
func importProfiles(ctx context.Context, tx *sql.Tx, profiles []BackupProfile) error {
	ids, _ := json.Marshal(func() []int64 {
		var out []int64
		for _, p := range profiles {
			out = append(out, p.ID)
		}
		return out
	}())
	epoch := time.Now().UnixNano()
	locks, err := tx.QueryContext(ctx, `SELECT d.id, p.name, p.kind FROM device d JOIN profile p ON p.id = d.profile_lock`)
	if err != nil {
		return err
	}
	type lock struct {
		device     int64
		name, kind string
	}
	var locked []lock
	for locks.Next() {
		var l lock
		if err := locks.Scan(&l.device, &l.name, &l.kind); err != nil {
			locks.Close()
			return err
		}
		locked = append(locked, l)
	}
	locks.Close()
	if err := locks.Err(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM profile WHERE id <> 1 AND id NOT IN (SELECT value FROM json_each(?))`, string(ids)); err != nil {
		return err
	}
	// Names are unique and may move from one id to another.
	if _, err := tx.ExecContext(ctx, `UPDATE profile SET name = '#' || id`); err != nil {
		return err
	}
	for _, p := range profiles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO profile (id, name, kind, pin_hash, pin_epoch, color, avatar, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO UPDATE SET name = excluded.name, kind = excluded.kind,
			pin_hash = excluded.pin_hash, pin_epoch = excluded.pin_epoch, color = excluded.color, avatar = excluded.avatar,
			created_at = excluded.created_at`, p.ID, p.Name, p.Kind, p.PINHash, epoch, p.Color, p.Avatar, p.CreatedAt); err != nil {
			return fmt.Errorf("profilo %q: %w", p.Name, err)
		}
	}
	// Profile 1 not in the backup (it never is missing, but a name must
	// not stay "#1").
	if _, err := tx.ExecContext(ctx, `UPDATE profile SET name = 'Principale', pin_epoch = ? WHERE id = 1 AND name = '#1'`, epoch); err != nil {
		return err
	}
	for _, l := range locked {
		if _, err := tx.ExecContext(ctx, `UPDATE device SET profile_lock = (SELECT id FROM profile WHERE name = ? AND kind = ?) WHERE id = ?`,
			l.name, l.kind, l.device); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM device WHERE id = ? AND profile_lock IS NULL`, l.device); err != nil {
			return err
		}
	}
	return nil
}

// ApplyPendingRestore attaches restored hidden items and watch progress to
// the items now in the catalog; what is still missing (a subscription not
// yet synchronised) stays pending. Downloads kept by a restore find their
// title again.
func (s *Store) ApplyPendingRestore(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, `UPDATE OR IGNORE download SET media_id = coalesce(CASE media
		WHEN 'movie' THEN (SELECT i.id FROM item i WHERE i.subscription_id = download.subscription_id AND i.kind = 'movie' AND i.source_id = download.source_id)
		ELSE (SELECT e.id FROM episode e JOIN item i ON i.id = e.series_item_id WHERE i.subscription_id = download.subscription_id AND e.source_id = download.source_id)
		END, media_id) WHERE media_id < 0`); err != nil {
		return err
	}
	var p pendingRestore
	if err := s.Setting(ctx, pendingKey, &p); err != nil || (len(p.Hidden) == 0 && len(p.Progress) == 0) {
		return err
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	itemID := func(r BackupItemRef) (int64, bool) {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM item WHERE subscription_id = ? AND kind = ? AND source_id = ?`,
			r.SubscriptionID, r.Kind, r.SourceID).Scan(&id)
		return id, err == nil
	}
	var left pendingRestore
	for _, h := range p.Hidden {
		id, ok := itemID(h)
		if !ok {
			left.Hidden = append(left.Hidden, h)
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_override (item_id, hidden) VALUES (?, 1)
			ON CONFLICT (item_id) DO UPDATE SET hidden = 1`, id); err != nil {
			return err
		}
	}
	for _, pr := range p.Progress {
		id, ok := itemID(pr.Item)
		if !ok {
			left.Progress = append(left.Progress, pr)
			continue
		}
		mediaID := id
		if pr.Media == "episode" {
			if pr.Episode == nil {
				continue
			}
			e := pr.Episode
			if _, err := tx.ExecContext(ctx, `INSERT INTO episode (series_item_id, source_id, season, number, title, extension, duration, plot)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (series_item_id, source_id) DO NOTHING`,
				id, e.SourceID, e.Season, e.Number, e.Title, e.Extension, e.Duration, e.Plot); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT id FROM episode WHERE series_item_id = ? AND source_id = ?`, id, e.SourceID).Scan(&mediaID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO progress (profile_id, media, media_id, position, duration, completed, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (profile_id, media, media_id) DO NOTHING`,
			pr.ProfileID, pr.Media, mediaID, pr.Position, pr.Duration, pr.Completed, pr.UpdatedAt); err != nil {
			return err
		}
	}
	if len(left.Hidden) == 0 && len(left.Progress) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM setting WHERE key = ?`, pendingKey); err != nil {
			return err
		}
	} else {
		raw, _ := json.Marshal(left)
		if _, err := tx.ExecContext(ctx, `UPDATE setting SET value = ? WHERE key = ?`, string(raw), pendingKey); err != nil {
			return err
		}
	}
	return tx.Commit()
}
