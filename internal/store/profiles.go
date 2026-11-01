package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Profile is a person (or a group) watching: own favourites and progress.
type Profile struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // admin, standard, kids
	PINHash   string `json:"-"`
	PINEpoch  int64  `json:"-"`
	HasPIN    bool   `json:"has_pin"`
	Color     string `json:"color"`
	Avatar    string `json:"avatar"` // an icon name; "" = the initial
	CreatedAt int64  `json:"created_at"`
}

const profileColumns = `id, name, kind, pin_hash, pin_epoch, color, avatar, created_at`

func scanProfile(row interface{ Scan(...any) error }) (Profile, error) {
	var p Profile
	err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.PINHash, &p.PINEpoch, &p.Color, &p.Avatar, &p.CreatedAt)
	p.HasPIN = p.PINHash != ""
	return p, err
}

func (s *Store) Profiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+profileColumns+` FROM profile ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Profile(ctx context.Context, id int64) (Profile, error) {
	p, err := scanProfile(s.r.QueryRowContext(ctx, `SELECT `+profileColumns+` FROM profile WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// ErrNameTaken is returned for a duplicate profile name.
var ErrNameTaken = errors.New("esiste già un profilo con questo nome")

func (s *Store) AddProfile(ctx context.Context, p Profile) (int64, error) {
	// SQLite reuses the id of a deleted profile: a new profile starts from
	// an epoch no earlier selection can carry, so an old profile token never
	// opens it.
	now := time.Now()
	res, err := s.w.ExecContext(ctx, `INSERT INTO profile (name, kind, pin_hash, pin_epoch, color, avatar, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Kind, p.PINHash, now.UnixNano(), p.Color, p.Avatar, now.Unix())
	if isUnique(err) {
		return 0, ErrNameTaken
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateProfile stores name, kind, colour, avatar and PIN; a PIN change bumps the
// epoch so earlier profile selections stop working.
func (s *Store) UpdateProfile(ctx context.Context, p Profile, pinChanged bool) error {
	bump := 0
	if pinChanged {
		bump = 1
	}
	res, err := s.w.ExecContext(ctx, `UPDATE profile SET name = ?, kind = ?, pin_hash = ?, color = ?, avatar = ?, pin_epoch = pin_epoch + ? WHERE id = ?`,
		p.Name, p.Kind, p.PINHash, p.Color, p.Avatar, bump, p.ID)
	if isUnique(err) {
		return ErrNameTaken
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProfile removes a profile with its favourites and progress.
func (s *Store) DeleteProfile(ctx context.Context, id int64) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM favorite WHERE profile_id = ?`, `DELETE FROM progress WHERE profile_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM profile WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// Device is a paired TV, phone or browser. The token is never stored,
// only its hash.
type Device struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ProfileLock *int64 `json:"profile_lock"`
	CreatedAt   int64  `json:"created_at"`
	LastSeen    int64  `json:"last_seen"`
}

const deviceColumns = `id, name, profile_lock, created_at, last_seen`

func scanDevice(row interface{ Scan(...any) error }) (Device, error) {
	var d Device
	var lock sql.NullInt64
	err := row.Scan(&d.ID, &d.Name, &lock, &d.CreatedAt, &d.LastSeen)
	if lock.Valid {
		d.ProfileLock = &lock.Int64
	}
	return d, err
}

func (s *Store) AddDevice(ctx context.Context, name string, tokenHash []byte) (int64, error) {
	res, err := s.w.ExecContext(ctx, `INSERT INTO device (name, token_hash, created_at) VALUES (?, ?, ?)`, name, tokenHash, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeviceByToken(ctx context.Context, tokenHash []byte) (Device, error) {
	d, err := scanDevice(s.r.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM device WHERE token_hash = ?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

func (s *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+deviceColumns+` FROM device ORDER BY last_seen DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDevice(ctx context.Context, id int64, name string, lock *int64) error {
	res, err := s.w.ExecContext(ctx, `UPDATE device SET name = ?, profile_lock = ? WHERE id = ?`, name, lock, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteDevice(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM device WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchDevice records activity (at most once a minute per device).
func (s *Store) TouchDevice(ctx context.Context, id int64, now time.Time) error {
	_, err := s.w.ExecContext(ctx, `UPDATE device SET last_seen = ? WHERE id = ? AND last_seen < ?`, now.Unix(), id, now.Unix()-60)
	return err
}

// ItemAdult reports whether an item belongs to an adult category.
func (s *Store) ItemAdult(ctx context.Context, id int64) (bool, error) {
	var adult bool
	err := s.r.QueryRowContext(ctx, `SELECT adult FROM item WHERE id = ?`, id).Scan(&adult)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return adult, err
}

// GroupAdult reports whether any variant of a channel or title is adult.
func (s *Store) GroupAdult(ctx context.Context, groupKey string) (bool, error) {
	var adult bool
	err := s.r.QueryRowContext(ctx, `SELECT coalesce(max(adult), 0) FROM item WHERE group_key = ?`, groupKey).Scan(&adult)
	return adult, err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
