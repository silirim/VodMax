package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Download is a film or episode being copied to the server's disk.
type Download struct {
	ID             int64  `json:"id"`
	Media          string `json:"media"` // movie or episode
	MediaID        int64  `json:"media_id"`
	SubscriptionID int64  `json:"subscription_id"`
	SourceID       string `json:"-"`
	Extension      string `json:"-"`
	Title          string `json:"title"`
	Path           string `json:"path"`
	Meta           string `json:"-"`
	Status         string `json:"status"`
	Bytes          int64  `json:"bytes"`
	Total          int64  `json:"total"`
	Error          string `json:"error,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

const downloadColumns = `id, media, media_id, subscription_id, source_id, extension, title, path, meta, status, bytes, total, error, created_at, updated_at`

func scanDownload(row interface{ Scan(...any) error }) (Download, error) {
	var d Download
	err := row.Scan(&d.ID, &d.Media, &d.MediaID, &d.SubscriptionID, &d.SourceID, &d.Extension, &d.Title, &d.Path, &d.Meta,
		&d.Status, &d.Bytes, &d.Total, &d.Error, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

// AddDownload queues a download; ok=false if that film or episode is
// already in the list.
func (s *Store) AddDownload(ctx context.Context, d Download) (int64, bool, error) {
	now := time.Now().Unix()
	// Two titles with the same name and year (two qualities, the same film
	// in two subscriptions) must not share a file: one would overwrite or
	// resume the other's. The second gets its id in the name, which Jellyfin
	// and Kodi show as another version ("1408 (2007) - 557.mp4").
	var taken bool
	if err := s.r.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM download WHERE path = ? AND NOT (media = ? AND media_id = ?))`,
		d.Path, d.Media, d.MediaID).Scan(&taken); err != nil {
		return 0, false, err
	}
	if taken {
		ext := path.Ext(d.Path)
		d.Path = fmt.Sprintf("%s - %d%s", strings.TrimSuffix(d.Path, ext), d.MediaID, ext)
	}
	res, err := s.w.ExecContext(ctx, `INSERT INTO download (media, media_id, subscription_id, source_id, extension, title, path, meta, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?, ?) ON CONFLICT (media, media_id) DO NOTHING`,
		d.Media, d.MediaID, d.SubscriptionID, d.SourceID, d.Extension, d.Title, d.Path, d.Meta, now, now)
	if err != nil {
		return 0, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var id int64
		err := s.r.QueryRowContext(ctx, `SELECT id FROM download WHERE media = ? AND media_id = ?`, d.Media, d.MediaID).Scan(&id)
		return id, false, err
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

func (s *Store) Downloads(ctx context.Context) ([]Download, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+downloadColumns+` FROM download
		ORDER BY CASE status WHEN 'downloading' THEN 0 WHEN 'queued' THEN 1 WHEN 'paused' THEN 2 WHEN 'failed' THEN 3 ELSE 4 END, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) Download(ctx context.Context, id int64) (Download, error) {
	d, err := scanDownload(s.r.QueryRowContext(ctx, `SELECT `+downloadColumns+` FROM download WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// DownloadOf is the download of a film or an episode (one at most).
func (s *Store) DownloadOf(ctx context.Context, media string, mediaID int64) (Download, error) {
	d, err := scanDownload(s.r.QueryRowContext(ctx, `SELECT `+downloadColumns+` FROM download WHERE media = ? AND media_id = ?`, media, mediaID))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// NextDownload is the oldest queued download, except the ids in skip.
func (s *Store) NextDownload(ctx context.Context, skip map[int64]bool) (Download, bool, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+downloadColumns+` FROM download WHERE status = 'queued' ORDER BY id`)
	if err != nil {
		return Download{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return d, false, err
		}
		if !skip[d.ID] {
			return d, true, nil
		}
	}
	return Download{}, false, rows.Err()
}

// SetDownloadState records status, progress and error.
func (s *Store) SetDownloadState(ctx context.Context, id int64, status string, bytes, total int64, errText string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE download SET status = ?, bytes = ?, total = ?, error = ?, updated_at = ? WHERE id = ?`,
		status, bytes, total, errText, time.Now().Unix(), id)
	return err
}

// RequeueInterrupted puts downloads left "downloading" by a restart back in
// the queue.
func (s *Store) RequeueInterrupted(ctx context.Context) error {
	_, err := s.w.ExecContext(ctx, `UPDATE download SET status = 'queued' WHERE status = 'downloading'`)
	return err
}

// DownloadFileShared tells whether another download uses the same file
// (two lists made before names were kept apart).
func (s *Store) DownloadFileShared(ctx context.Context, id int64, path string) (bool, error) {
	var shared bool
	err := s.r.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM download WHERE path = ? AND id <> ?)`, path, id).Scan(&shared)
	return shared, err
}

func (s *Store) DeleteDownload(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, `DELETE FROM download WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ItemBasics are the title, year and image of a film or series.
type ItemBasics struct {
	ID             int64
	SubscriptionID int64
	Kind           string
	Title          string
	Year           int
	Image          string
}

func (s *Store) ItemBasics(ctx context.Context, id int64) (ItemBasics, error) {
	var b ItemBasics
	err := s.r.QueryRowContext(ctx, `SELECT id, subscription_id, kind, display_title, coalesce(year, 0), image FROM item WHERE id = ?`, id).
		Scan(&b.ID, &b.SubscriptionID, &b.Kind, &b.Title, &b.Year, &b.Image)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}
