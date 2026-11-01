package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// VPN is a stored WireGuard connection. Config is sealed.
type VPN struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"` // wireguard or system
	CreatedAt time.Time `json:"created_at"`
	Config    []byte    `json:"-"`
}

// Subscription is a provider playlist, or a local collection (kind
// "local": a folder, no VPN, VPNID 0). Secret is sealed and holds the
// Xtream password or the M3U URL (which usually embeds credentials).
type Subscription struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	VPNID      int64           `json:"vpn_id"`
	Settings   json.RawMessage `json:"settings"`
	CreatedAt  time.Time       `json:"created_at"`
	SyncedAt   *time.Time      `json:"synced_at,omitempty"`
	SyncStatus string          `json:"sync_status"`
	SyncError  string          `json:"sync_error,omitempty"`
	Account    json.RawMessage `json:"account"`
	Secret     []byte          `json:"-"`
}

// ErrVPNName: two VPN connections with one name.
var ErrVPNName = errors.New("esiste già una connessione VPN con questo nome")

// AddVPN stores a connection; seal receives the new row id so the sealed
// value is bound to it.
func (s *Store) AddVPN(ctx context.Context, name, kind string, seal func(id int64) []byte) (int64, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO vpn (name, kind, config, created_at) VALUES (?, ?, x'', ?)`, name, kind, time.Now().Unix())
	if isUnique(err) {
		return 0, ErrVPNName
	}
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `UPDATE vpn SET config = ? WHERE id = ?`, seal(id), id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) VPNs(ctx context.Context) ([]VPN, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, name, kind, created_at, config FROM vpn ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VPN
	for rows.Next() {
		var v VPN
		var created int64
		if err := rows.Scan(&v.ID, &v.Name, &v.Kind, &created, &v.Config); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(created, 0)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) VPN(ctx context.Context, id int64) (VPN, error) {
	var v VPN
	var created int64
	err := s.r.QueryRowContext(ctx, `SELECT id, name, kind, created_at, config FROM vpn WHERE id = ?`, id).
		Scan(&v.ID, &v.Name, &v.Kind, &created, &v.Config)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	v.CreatedAt = time.Unix(created, 0)
	return v, err
}

// ErrInUse is returned when deleting a VPN still used by a subscription.
var ErrInUse = errors.New("connessione VPN usata da un abbonamento")

func (s *Store) DeleteVPN(ctx context.Context, id int64) error {
	var n int
	if err := s.w.QueryRowContext(ctx, `SELECT count(*) FROM subscription WHERE vpn_id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrInUse
	}
	res, err := s.w.ExecContext(ctx, `DELETE FROM vpn WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddSubscription stores a subscription; seal is called with the new id.
// ErrSubscriptionName: two subscriptions or collections with one name.
var ErrSubscriptionName = errors.New("esiste già un abbonamento o una raccolta con questo nome")

func (s *Store) AddSubscription(ctx context.Context, sub Subscription, seal func(id int64) []byte) (int64, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO subscription (name, kind, vpn_id, settings, secret, created_at) VALUES (?, ?, ?, ?, x'', ?)`,
		sub.Name, sub.Kind, nullID(sub.VPNID), string(sub.Settings), time.Now().Unix())
	if isUnique(err) {
		return 0, ErrSubscriptionName
	}
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `UPDATE subscription SET secret = ? WHERE id = ?`, seal(id), id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

const subscriptionColumns = `id, name, kind, coalesce(vpn_id, 0), settings, created_at, synced_at, sync_status, sync_error, account, secret`

func scanSubscription(row interface{ Scan(...any) error }) (Subscription, error) {
	var sub Subscription
	var created int64
	var synced sql.NullInt64
	var settings, account string
	err := row.Scan(&sub.ID, &sub.Name, &sub.Kind, &sub.VPNID, &settings, &created, &synced,
		&sub.SyncStatus, &sub.SyncError, &account, &sub.Secret)
	if err != nil {
		return sub, err
	}
	sub.Settings, sub.Account = json.RawMessage(settings), json.RawMessage(account)
	sub.CreatedAt = time.Unix(created, 0)
	if synced.Valid {
		t := time.Unix(synced.Int64, 0)
		sub.SyncedAt = &t
	}
	return sub, nil
}

func (s *Store) Subscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+subscriptionColumns+` FROM subscription ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *Store) Subscription(ctx context.Context, id int64) (Subscription, error) {
	sub, err := scanSubscription(s.r.QueryRowContext(ctx, `SELECT `+subscriptionColumns+` FROM subscription WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return sub, ErrNotFound
	}
	return sub, err
}

func (s *Store) DeleteSubscription(ctx context.Context, id int64) error {
	if err := s.DeleteGuide(ctx, fmt.Sprintf("sub:%d", id)); err != nil {
		return err
	}
	res, err := s.w.ExecContext(ctx, `DELETE FROM subscription WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.w.ExecContext(ctx, `INSERT INTO item_search(item_search) VALUES ('rebuild')`)
	return err
}

// SetSyncResult records the outcome of a synchronisation attempt. A nil
// account keeps the last known account state (e.g. after a failure).
func (s *Store) SetSyncResult(ctx context.Context, id int64, status, errText string, account json.RawMessage) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE subscription SET synced_at = ?, sync_status = ?, sync_error = ?, account = coalesce(?, account) WHERE id = ?`,
		time.Now().Unix(), status, errText, nullable(account), id)
	return err
}

// nullID stores 0 as NULL (no VPN).
func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func nullable(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}

// UpdateSubscription changes name, VPN, settings and sealed secret; nil
// arguments are left unchanged.
func (s *Store) UpdateSubscription(ctx context.Context, id int64, name *string, vpnID *int64, settings json.RawMessage, secret []byte) error {
	res, err := s.w.ExecContext(ctx, `UPDATE subscription SET name = coalesce(?, name), vpn_id = coalesce(?, vpn_id),
		settings = coalesce(?, settings), secret = coalesce(?, secret) WHERE id = ?`,
		name, vpnID, nullable(settings), secret, id)
	if isUnique(err) {
		return ErrSubscriptionName
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateVPN changes the name and/or the sealed configuration.
func (s *Store) UpdateVPN(ctx context.Context, id int64, name *string, config []byte) error {
	res, err := s.w.ExecContext(ctx, `UPDATE vpn SET name = coalesce(?, name), config = coalesce(?, config) WHERE id = ?`,
		name, config, id)
	if isUnique(err) {
		return ErrVPNName
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
