// Package store persists VPN connections, subscriptions and the catalog in
// SQLite. Reads use their own connection pool (WAL), so a catalog import
// never blocks browsing.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store holds one writer connection and a pool of read-only connections.
type Store struct {
	w    *sql.DB
	r    *sql.DB
	path string
}

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("non trovato")

// Open creates or migrates the database at path.
func Open(path string) (*Store, error) {
	dsn := func(extra string) string {
		q := url.Values{}
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "busy_timeout(10000)")
		q.Add("_pragma", "foreign_keys(1)")
		q.Add("_pragma", "synchronous(NORMAL)")
		if extra != "" {
			q.Add("_pragma", extra)
		}
		return "file:" + path + "?" + q.Encode()
	}
	w, err := sql.Open("sqlite", dsn(""))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	r, err := sql.Open("sqlite", dsn("query_only(1)"))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(max(4, runtime.NumCPU()))
	s := &Store{w: w, r: r, path: path}
	if err := s.migrate(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close releases both pools.
func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

var migrations = []string{
	// 1: VPN, subscriptions, catalog.
	`CREATE TABLE vpn (
		id         INTEGER PRIMARY KEY,
		name       TEXT NOT NULL UNIQUE,
		config     BLOB NOT NULL,           -- sealed wg-quick text
		created_at INTEGER NOT NULL
	);
	CREATE TABLE subscription (
		id          INTEGER PRIMARY KEY,
		name        TEXT NOT NULL UNIQUE,
		kind        TEXT NOT NULL CHECK (kind IN ('xtream', 'm3u')),
		vpn_id      INTEGER NOT NULL REFERENCES vpn(id),
		settings    TEXT NOT NULL,           -- JSON, no secrets
		secret      BLOB NOT NULL,           -- sealed password or playlist URL
		created_at  INTEGER NOT NULL,
		synced_at   INTEGER,
		sync_status TEXT NOT NULL DEFAULT 'never',
		sync_error  TEXT NOT NULL DEFAULT '',
		account     TEXT NOT NULL DEFAULT '{}' -- JSON: provider account state
	);
	CREATE TABLE category (
		subscription_id INTEGER NOT NULL REFERENCES subscription(id) ON DELETE CASCADE,
		kind            TEXT NOT NULL,
		source_id       TEXT NOT NULL,
		name            TEXT NOT NULL,
		PRIMARY KEY (subscription_id, kind, source_id)
	);
	CREATE TABLE item (
		id              INTEGER PRIMARY KEY,
		subscription_id INTEGER NOT NULL REFERENCES subscription(id) ON DELETE CASCADE,
		kind            TEXT NOT NULL CHECK (kind IN ('live', 'movie', 'series')),
		source_id       TEXT NOT NULL,
		title           TEXT NOT NULL,       -- as published by the provider
		display_title   TEXT NOT NULL,       -- without quality tags / year
		year            INTEGER,
		category_id     TEXT NOT NULL DEFAULT '',
		image           TEXT NOT NULL DEFAULT '',
		epg_id          TEXT NOT NULL DEFAULT '',
		number          INTEGER,
		rating          REAL,
		tmdb_id         TEXT NOT NULL DEFAULT '',
		extension       TEXT NOT NULL DEFAULT '',
		catchup_days    INTEGER NOT NULL DEFAULT 0,
		added_at        INTEGER,
		locator         BLOB,                -- sealed stream URL (M3U only)
		first_seen_at   INTEGER NOT NULL,
		last_seen_at    INTEGER NOT NULL,
		available       INTEGER NOT NULL DEFAULT 1,
		UNIQUE (subscription_id, kind, source_id)
	);
	CREATE INDEX item_by_title ON item (kind, available, display_title COLLATE NOCASE, id);
	CREATE INDEX item_by_arrival ON item (kind, available, first_seen_at DESC, id);
	CREATE INDEX item_by_category ON item (subscription_id, kind, category_id, available);
	CREATE VIRTUAL TABLE item_search USING fts5 (
		display_title, content = 'item', content_rowid = 'id',
		tokenize = 'unicode61 remove_diacritics 2', prefix = '2 3'
	);`,
	// 2: catalog selection. Classification is recomputed at every import;
	// user choices live in separate tables so they survive re-imports.
	`ALTER TABLE subscription ADD COLUMN rules TEXT NOT NULL DEFAULT '{}';
	ALTER TABLE category ADD COLUMN country TEXT NOT NULL DEFAULT '';
	ALTER TABLE category ADD COLUMN adult INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE category ADD COLUMN low_res INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE category ADD COLUMN section TEXT NOT NULL DEFAULT '';
	CREATE TABLE category_override (
		subscription_id INTEGER NOT NULL REFERENCES subscription(id) ON DELETE CASCADE,
		kind            TEXT NOT NULL,
		source_id       TEXT NOT NULL,
		enabled         INTEGER,             -- NULL: decided by the rules
		section         TEXT,
		country         TEXT,
		adult           INTEGER,
		PRIMARY KEY (subscription_id, kind, source_id)
	);
	ALTER TABLE item ADD COLUMN quality INTEGER NOT NULL DEFAULT 2;
	ALTER TABLE item ADD COLUMN group_key TEXT NOT NULL DEFAULT '';
	ALTER TABLE item ADD COLUMN section TEXT NOT NULL DEFAULT '';
	ALTER TABLE item ADD COLUMN visible INTEGER NOT NULL DEFAULT 1;
	ALTER TABLE item ADD COLUMN primary_variant INTEGER NOT NULL DEFAULT 1;
	ALTER TABLE item ADD COLUMN variant_count INTEGER NOT NULL DEFAULT 1;
	CREATE TABLE item_override (
		item_id INTEGER PRIMARY KEY REFERENCES item(id) ON DELETE CASCADE,
		hidden  INTEGER NOT NULL
	);
	CREATE INDEX item_browse ON item (kind, visible, primary_variant, display_title COLLATE NOCASE, id);
	CREATE INDEX item_browse_recent ON item (kind, visible, primary_variant, first_seen_at DESC, id);
	CREATE INDEX item_variants ON item (kind, group_key, visible);`,
	// 3: TV: official numbering, programme guide, favourites.
	`ALTER TABLE item ADD COLUMN lcn INTEGER;          -- official channel number, when recognised
	ALTER TABLE item ADD COLUMN name_key TEXT NOT NULL DEFAULT ''; -- channel name for guide matching
	CREATE TABLE epg_channel (
		subscription_id INTEGER NOT NULL REFERENCES subscription(id) ON DELETE CASCADE,
		id              TEXT NOT NULL,               -- XMLTV channel id, lower case
		name_key        TEXT NOT NULL,
		PRIMARY KEY (subscription_id, id)
	);
	CREATE INDEX epg_channel_by_name ON epg_channel (name_key);
	CREATE TABLE epg_programme (
		channel     TEXT NOT NULL,
		start       INTEGER NOT NULL,
		stop        INTEGER NOT NULL,
		title       TEXT NOT NULL,
		subtitle    TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		category    TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (channel, start)
	) WITHOUT ROWID;
	CREATE INDEX epg_programme_by_stop ON epg_programme (channel, stop);
	CREATE TABLE favorite (
		profile_id INTEGER NOT NULL,
		kind       TEXT NOT NULL,
		group_key  TEXT NOT NULL,                    -- the channel or title, not one variant
		position   INTEGER NOT NULL,
		added_at   INTEGER NOT NULL,
		PRIMARY KEY (profile_id, kind, group_key)
	);`,
	// 4: images (channel logos, covers) fetched through the VPN.
	`CREATE TABLE image_cache (
		url_hash     TEXT PRIMARY KEY,          -- sha256 of the source URL
		content_type TEXT NOT NULL,             -- '' when the source failed
		data         BLOB,
		fetched_at   INTEGER NOT NULL
	);`,
	// 5: episodes (loaded when a series is opened), watch progress,
	// recordings.
	`CREATE TABLE episode (
		id             INTEGER PRIMARY KEY,
		series_item_id INTEGER NOT NULL REFERENCES item(id) ON DELETE CASCADE,
		source_id      TEXT NOT NULL,
		season         INTEGER NOT NULL,
		number         INTEGER NOT NULL,
		title          TEXT NOT NULL,
		extension      TEXT NOT NULL DEFAULT '',
		duration       INTEGER NOT NULL DEFAULT 0,  -- seconds, 0 if unknown
		plot           TEXT NOT NULL DEFAULT '',
		UNIQUE (series_item_id, source_id)
	);
	CREATE INDEX episode_order ON episode (series_item_id, season, number);
	CREATE TABLE series_fetch (
		series_item_id INTEGER PRIMARY KEY REFERENCES item(id) ON DELETE CASCADE,
		fetched_at     INTEGER NOT NULL
	);
	CREATE TABLE progress (
		profile_id INTEGER NOT NULL,
		media      TEXT NOT NULL CHECK (media IN ('movie', 'episode')),
		media_id   INTEGER NOT NULL,
		position   INTEGER NOT NULL,              -- seconds
		duration   INTEGER NOT NULL,
		completed  INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (profile_id, media, media_id)
	);
	CREATE INDEX progress_recent ON progress (profile_id, completed, updated_at DESC);
	CREATE TABLE recording (
		id         INTEGER PRIMARY KEY,
		item_id    INTEGER NOT NULL,              -- the channel (primary variant when scheduled)
		group_key  TEXT NOT NULL,                 -- the channel, for when variants change
		channel    TEXT NOT NULL,
		title      TEXT NOT NULL,
		start      INTEGER NOT NULL,
		stop       INTEGER NOT NULL,
		status     TEXT NOT NULL CHECK (status IN ('scheduled', 'recording', 'completed', 'failed', 'cancelled')),
		file       TEXT NOT NULL DEFAULT '',
		bytes      INTEGER NOT NULL DEFAULT 0,
		error      TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE INDEX recording_due ON recording (status, start);`,
	// 6: server settings and bookkeeping (schedules, last runs).
	`CREATE TABLE setting (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`,
	// 7: profiles, paired devices, adult flag per item (for kids profiles).
	`CREATE TABLE profile (
		id         INTEGER PRIMARY KEY,
		name       TEXT NOT NULL UNIQUE,
		kind       TEXT NOT NULL CHECK (kind IN ('admin', 'standard', 'kids')),
		pin_hash   TEXT NOT NULL DEFAULT '',   -- '' = no PIN
		pin_epoch  INTEGER NOT NULL DEFAULT 0, -- changes invalidate profile selections
		color      TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	INSERT INTO profile (id, name, kind, created_at) VALUES (1, 'Principale', 'admin', strftime('%s', 'now'));
	CREATE TABLE device (
		id           INTEGER PRIMARY KEY,
		name         TEXT NOT NULL,
		token_hash   BLOB NOT NULL UNIQUE,     -- sha256 of the device token
		profile_lock INTEGER REFERENCES profile(id) ON DELETE SET NULL, -- only this profile, if set
		created_at   INTEGER NOT NULL,
		last_seen    INTEGER NOT NULL DEFAULT 0
	);
	ALTER TABLE item ADD COLUMN adult INTEGER NOT NULL DEFAULT 0;
	CREATE INDEX recording_group ON recording (group_key);`,
	// 8: metadata of films and series (provider details, TMDB), cached.
	`CREATE TABLE meta_cache (
		key        TEXT PRIMARY KEY,   -- "p:<item id>" provider, "t:movie:<id>" / "t:tv:<id>" TMDB
		data       TEXT NOT NULL,      -- JSON; '' when the source had nothing
		fetched_at INTEGER NOT NULL
	);`,
	// 9: guide channels by source ("sub:<id>" provider, "ext:<id>" external
	// XMLTV), external guide sources.
	`CREATE TABLE epg_channel_new (
		source   TEXT NOT NULL,
		id       TEXT NOT NULL,               -- XMLTV channel id, lower case
		name_key TEXT NOT NULL,
		PRIMARY KEY (source, id)
	);
	INSERT INTO epg_channel_new SELECT 'sub:' || subscription_id, id, name_key FROM epg_channel;
	DROP TABLE epg_channel;
	ALTER TABLE epg_channel_new RENAME TO epg_channel;
	CREATE INDEX epg_channel_by_name ON epg_channel (name_key);
	CREATE INDEX epg_channel_by_id ON epg_channel (id);
	ALTER TABLE epg_programme ADD COLUMN source TEXT NOT NULL DEFAULT ''; -- '' for rows of older versions
	CREATE INDEX epg_programme_by_source ON epg_programme (source);
	CREATE TABLE guide_source (
		id         INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		url        TEXT NOT NULL,
		enabled    INTEGER NOT NULL DEFAULT 1,
		synced_at  INTEGER,
		status     TEXT NOT NULL DEFAULT 'never',
		error      TEXT NOT NULL DEFAULT '',
		channels   INTEGER NOT NULL DEFAULT 0,
		programmes INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);`,
	// 10: recording rules, protected recordings.
	`CREATE TABLE recording_rule (
		id         INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		spec       TEXT NOT NULL,            -- JSON: words, channel, section
		keep       INTEGER NOT NULL DEFAULT 0, -- completed recordings to keep, 0 = all
		enabled    INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL,
		last_run   INTEGER,
		last_note  TEXT NOT NULL DEFAULT ''
	);
	ALTER TABLE recording ADD COLUMN rule_id INTEGER;
	ALTER TABLE recording ADD COLUMN protected INTEGER NOT NULL DEFAULT 0;`,
	// 11: VPN kind: "wireguard" (internal tunnel) or "system" (a VPN the
	// user runs outside VodMax, on the device or the router).
	`ALTER TABLE vpn ADD COLUMN kind TEXT NOT NULL DEFAULT 'wireguard' CHECK (kind IN ('wireguard', 'system'));`,
	// 12: local collections: a subscription of kind "local" reads a
	// folder and has no VPN. SQLite cannot change a CHECK or NOT NULL, so
	// the table is rebuilt (with foreign keys off: see noForeignKeys).
	`CREATE TABLE subscription_new (
		id          INTEGER PRIMARY KEY,
		name        TEXT NOT NULL UNIQUE,
		kind        TEXT NOT NULL CHECK (kind IN ('xtream', 'm3u', 'local')),
		vpn_id      INTEGER REFERENCES vpn(id),   -- NULL for local collections
		settings    TEXT NOT NULL,
		secret      BLOB NOT NULL,
		created_at  INTEGER NOT NULL,
		synced_at   INTEGER,
		sync_status TEXT NOT NULL DEFAULT 'never',
		sync_error  TEXT NOT NULL DEFAULT '',
		account     TEXT NOT NULL DEFAULT '{}',
		rules       TEXT NOT NULL DEFAULT '{}'
	);
	INSERT INTO subscription_new (id, name, kind, vpn_id, settings, secret, created_at, synced_at, sync_status, sync_error, account, rules)
		SELECT id, name, kind, vpn_id, settings, secret, created_at, synced_at, sync_status, sync_error, account, rules FROM subscription;
	DROP TABLE subscription;
	ALTER TABLE subscription_new RENAME TO subscription;`,
	// 13: downloads of films and episodes to the server's disk.
	`CREATE TABLE download (
		id              INTEGER PRIMARY KEY,
		media           TEXT NOT NULL CHECK (media IN ('movie', 'episode')),
		media_id        INTEGER NOT NULL,      -- the film item or the episode
		subscription_id INTEGER NOT NULL,
		source_id       TEXT NOT NULL,
		extension       TEXT NOT NULL DEFAULT '',
		title           TEXT NOT NULL,
		path            TEXT NOT NULL,         -- inside the downloads folder
		meta            TEXT NOT NULL DEFAULT '{}', -- JSON: what the .nfo and the poster need
		status          TEXT NOT NULL CHECK (status IN ('queued', 'downloading', 'paused', 'done', 'failed')),
		bytes           INTEGER NOT NULL DEFAULT 0,
		total           INTEGER NOT NULL DEFAULT 0,
		error           TEXT NOT NULL DEFAULT '',
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL,
		UNIQUE (media, media_id)
	);
	CREATE INDEX download_queue ON download (status, id);`,
	// 14: the avatar a person chooses for their profile.
	`ALTER TABLE profile ADD COLUMN avatar TEXT NOT NULL DEFAULT ''`,
	// 15: what the guide says about a programme: live, rerun, premiere, new.
	`ALTER TABLE epg_programme ADD COLUMN airing TEXT NOT NULL DEFAULT ''`,
	// 16: where the opening and the end credits of a series are, learned
	// from the people watching it (seconds).
	`CREATE TABLE series_marker (
		series_key  TEXT PRIMARY KEY,                -- group key of the series
		intro_start INTEGER NOT NULL DEFAULT 0,
		intro_end   INTEGER NOT NULL DEFAULT 0,      -- 0: not known
		credits     INTEGER NOT NULL DEFAULT 0,      -- length of the end credits, 0: not known
		updated_at  INTEGER NOT NULL
	)`,
	// 17: a picture of each episode; the episodes are loaded again to get it.
	`ALTER TABLE episode ADD COLUMN image TEXT NOT NULL DEFAULT '';
	DELETE FROM series_fetch`,
	// 18: Stalker portals: the kind is allowed by rebuilding the table, as
	// in migration 12.
	`CREATE TABLE subscription_new (
		id          INTEGER PRIMARY KEY,
		name        TEXT NOT NULL UNIQUE,
		kind        TEXT NOT NULL CHECK (kind IN ('xtream', 'm3u', 'local', 'stalker')),
		vpn_id      INTEGER REFERENCES vpn(id),   -- NULL for local collections
		settings    TEXT NOT NULL,
		secret      BLOB NOT NULL,
		created_at  INTEGER NOT NULL,
		synced_at   INTEGER,
		sync_status TEXT NOT NULL DEFAULT 'never',
		sync_error  TEXT NOT NULL DEFAULT '',
		account     TEXT NOT NULL DEFAULT '{}',
		rules       TEXT NOT NULL DEFAULT '{}'
	);
	INSERT INTO subscription_new (id, name, kind, vpn_id, settings, secret, created_at, synced_at, sync_status, sync_error, account, rules)
		SELECT id, name, kind, vpn_id, settings, secret, created_at, synced_at, sync_status, sync_error, account, rules FROM subscription;
	DROP TABLE subscription;
	ALTER TABLE subscription_new RENAME TO subscription;`,
	// 19: the people of films and series (cast and director) from their
	// detail pages, for "a film with…". Rebuilt by itself, not backed up.
	`CREATE TABLE item_people (
		item_id    INTEGER PRIMARY KEY REFERENCES item(id) ON DELETE CASCADE,
		people     TEXT NOT NULL,      -- folded names between " , "; '' when the page had none
		checked_at INTEGER NOT NULL
	)`,
}

// noForeignKeys lists migrations (1-based) that rebuild a parent table:
// with foreign keys on, dropping the old table would delete its children.
var noForeignKeys = map[int]bool{12: true, 18: true}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	err := s.w.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.w.ExecContext(ctx, `INSERT INTO schema_version VALUES (0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database creato da una versione più recente (schema %d)", version)
	}
	if version > 0 && version < len(migrations) {
		if err := s.copyBeforeMigrating(ctx, version); err != nil {
			return fmt.Errorf("copia del database prima dell'aggiornamento: %w", err)
		}
	}
	for i := version; i < len(migrations); i++ {
		if err := s.migrateOne(ctx, i+1, migrations[i]); err != nil {
			return err
		}
	}
	return nil
}

// keepPreUpgrade is how many copies made before migrating are kept.
const keepPreUpgrade = 3

// copyBeforeMigrating saves the database as it is, next to it in
// pre-upgrade/, before a newer version changes its schema: the previous
// version cannot open a migrated database, the copy it can.
func (s *Store) copyBeforeMigrating(ctx context.Context, version int) error {
	if s.path == "" || strings.HasPrefix(s.path, ":memory:") {
		return nil
	}
	dir := filepath.Join(filepath.Dir(s.path), "pre-upgrade")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := filepath.Join(dir, fmt.Sprintf("vodmax-schema%d-%s.db", version, time.Now().Format("20060102-150405")))
	if _, err := s.w.ExecContext(ctx, `VACUUM INTO ?`, name); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(dir, "vodmax-schema*.db"))
	sort.Slice(old, func(i, j int) bool {
		a, _ := os.Stat(old[i])
		b, _ := os.Stat(old[j])
		return a != nil && b != nil && a.ModTime().After(b.ModTime())
	})
	for _, f := range old[min(len(old), keepPreUpgrade):] {
		os.Remove(f)
	}
	return nil
}

func (s *Store) migrateOne(ctx context.Context, n int, script string) error {
	// The writer pool has one connection: the pragma applies to it.
	if noForeignKeys[n] {
		if _, err := s.w.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}
		defer s.w.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`)
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("migrazione %d: %w", n, err)
	}
	if noForeignKeys[n] {
		var broken int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&broken); err != nil {
			return err
		}
		if broken > 0 {
			return fmt.Errorf("migrazione %d: %d riferimenti non validi", n, broken)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, n); err != nil {
		return err
	}
	return tx.Commit()
}
