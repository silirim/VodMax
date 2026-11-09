package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/silirim/vodmax/internal/store"
)

// Recordings are deleted automatically only by rules the user sets: a
// size limit, an age, the number to keep per recording rule. Protected
// recordings are never deleted automatically. A nearly full disk is
// reported, and below minFreeToRecord no recording starts.

const (
	recordingsKey   = "recordings"
	minFreeToRecord = 1 << 30
	lowDiskWarning  = 5 << 30
	cancelledKeep   = 7 * 24 * time.Hour
)

// RecordingSettings limit the space recordings use.
type RecordingSettings struct {
	MaxGB    int `json:"max_gb"`    // 0 = no limit
	KeepDays int `json:"keep_days"` // 0 = forever
}

func (s *Service) RecordingSettings(ctx context.Context) (RecordingSettings, error) {
	var rs RecordingSettings
	err := s.st.Setting(ctx, recordingsKey, &rs)
	return rs, err
}

func (s *Service) SetRecordingSettings(ctx context.Context, rs RecordingSettings) error {
	if rs.MaxGB < 0 || rs.MaxGB > 100000 || rs.KeepDays < 0 || rs.KeepDays > 3650 {
		return invalid("valori non validi")
	}
	if err := s.st.SetSetting(ctx, recordingsKey, rs); err != nil {
		return err
	}
	s.cleanupRecordings(ctx, nil)
	return nil
}

// Space describes the recordings' disk.
type Space struct {
	RecordingSettings
	UsedBytes  int64 `json:"used_bytes"`
	FreeBytes  int64 `json:"free_bytes"`  // -1 if unknown
	TotalBytes int64 `json:"total_bytes"` // -1 if unknown
	Count      int   `json:"count"`
}

func (s *Service) Space(ctx context.Context) (Space, error) {
	rs, err := s.RecordingSettings(ctx)
	if err != nil {
		return Space{}, err
	}
	sp := Space{RecordingSettings: rs, FreeBytes: -1, TotalBytes: -1}
	list, err := s.Recordings(ctx)
	if err != nil {
		return sp, err
	}
	for _, r := range list {
		if r.Status == "completed" || r.Status == "recording" || r.Status == "failed" {
			sp.UsedBytes += r.Bytes
			sp.Count++
		}
	}
	if free, total, ok := diskSpace(s.recordingsDir()); ok {
		sp.FreeBytes, sp.TotalBytes = free, total
	}
	return sp, nil
}

// recordingsDir is "" without a recorder (the Android app's own server).
func (s *Service) recordingsDir() string {
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	return s.rec.dir
}

func (s *Service) SetRecordingProtected(ctx context.Context, id int64, protected bool) error {
	return s.st.SetRecordingProtected(ctx, id, protected)
}

// cleanupRecordings applies the limits, oldest first.
func (s *Service) cleanupRecordings(ctx context.Context, log *slog.Logger) {
	s.st.PurgeCancelled(ctx, time.Now().Add(-cancelledKeep).Unix())
	rs, err := s.RecordingSettings(ctx)
	if err != nil {
		return
	}
	done, err := s.st.CompletedRecordings(ctx)
	if err != nil {
		return
	}
	rules, err := s.st.RecordingRules(ctx)
	if err != nil {
		return
	}
	keep := map[int64]int{}
	for _, r := range rules {
		keep[r.ID] = r.Keep
	}
	drop := map[int64]string{}
	// Per rule: the most recent Keep.
	perRule := map[int64][]store.Recording{}
	for _, r := range done {
		if r.RuleID != 0 && r.Status == "completed" {
			perRule[r.RuleID] = append(perRule[r.RuleID], r)
		}
	}
	for rule, list := range perRule {
		if n := keep[rule]; n > 0 && len(list) > n {
			for _, r := range list[:len(list)-n] { // oldest first
				if !r.Protected {
					drop[r.ID] = "regola: tenute le ultime " + fmt.Sprint(n)
				}
			}
		}
	}
	if rs.KeepDays > 0 {
		limit := time.Now().AddDate(0, 0, -rs.KeepDays).Unix()
		for _, r := range done {
			if !r.Protected && r.Stop < limit {
				drop[r.ID] = fmt.Sprintf("più vecchia di %d giorni", rs.KeepDays)
			}
		}
	}
	if rs.MaxGB > 0 {
		var used int64
		for _, r := range done {
			if drop[r.ID] == "" {
				used += r.Bytes
			}
		}
		max := int64(rs.MaxGB) << 30
		for _, r := range done {
			if used <= max {
				break
			}
			if !r.Protected && drop[r.ID] == "" {
				drop[r.ID] = fmt.Sprintf("oltre il limite di %d GB", rs.MaxGB)
				used -= r.Bytes
			}
		}
	}
	for _, r := range done {
		why := drop[r.ID]
		if why == "" {
			continue
		}
		if err := s.DeleteRecording(ctx, r.ID); err == nil && log != nil {
			log.Info("registrazione eliminata automaticamente", "titolo", r.Title, "motivo", why)
		}
	}
}

// diskProblems for the status page.
func (s *Service) diskProblems(ctx context.Context) []Problem {
	free, _, ok := diskSpace(s.recordingsDir())
	switch {
	case !ok:
		return nil
	case free < minFreeToRecord:
		return []Problem{{Level: "error", Area: "disk", Text: T(ctx, "Disco delle registrazioni pieno (%.1f GB liberi): le registrazioni non partono", float64(free)/(1<<30))}}
	case free < lowDiskWarning:
		return []Problem{{Level: "warning", Area: "disk", Text: T(ctx, "Disco delle registrazioni quasi pieno: %.1f GB liberi", float64(free)/(1<<30))}}
	}
	return nil
}
