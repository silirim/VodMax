package service

import (
	"context"
	"strings"
	"time"

	"github.com/silirim/vodmax/internal/store"
)

// GuideRow is one channel of the programme grid.
type GuideRow struct {
	Channel    store.TVChannel `json:"channel"`
	Programmes []GuideEntry    `json:"programmes"`
}

// GuideEntry is a programme; Recording is the id of a scheduled or
// running recording that covers it.
type GuideEntry struct {
	store.Programme
	Recording int64 `json:"recording,omitempty"`
}

const maxGuideHours = 12

// Guide returns the programmes of the listed channels between from and
// from+hours, in remote-control order.
func (s *Service) Guide(ctx context.Context, from time.Time, hours int, section string, favorites bool) ([]GuideRow, error) {
	if hours <= 0 || hours > maxGuideHours {
		hours = 4
	}
	to := from.Add(time.Duration(hours) * time.Hour)
	channels, err := s.tvChannels(ctx, section, favorites) // no folders: the guide does not use them
	if err != nil {
		return nil, err
	}
	var guides []string
	for _, c := range channels {
		if c.GuideID != "" {
			guides = append(guides, c.GuideID)
		}
	}
	progs, err := s.st.GuideWindow(ctx, guides, from, to)
	if err != nil {
		return nil, err
	}
	recs, _, err := s.st.ActiveRecordingsBetween(ctx, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	byGroup := map[string][]store.Recording{}
	for _, r := range recs {
		byGroup[r.GroupKey] = append(byGroup[r.GroupKey], r)
	}
	out := make([]GuideRow, 0, len(channels))
	for _, c := range channels {
		row := GuideRow{Channel: c, Programmes: []GuideEntry{}}
		row.Channel.Now, row.Channel.Next = nil, nil
		for _, p := range progs[c.GuideID] {
			e := GuideEntry{Programme: p}
			for _, r := range byGroup[c.GroupKey] {
				// Recordings have margins around the programme.
				if r.Start <= p.Start+60 && r.Stop >= p.Stop-60 {
					e.Recording = r.ID
				}
			}
			row.Programmes = append(row.Programmes, e)
		}
		out = append(out, row)
	}
	return out, nil
}

// recordingConflict explains why a recording of group between start and
// stop would exceed the subscription's connections ("" if it fits).
func (s *Service) recordingConflict(ctx context.Context, subID int64, group string, start, stop int64) (string, error) {
	max, err := s.maxConnections(ctx, subID)
	if err != nil || max <= 0 {
		return "", err
	}
	list, subs, err := s.st.ActiveRecordingsBetween(ctx, start, stop)
	if err != nil {
		return "", err
	}
	var busy []string
	for i, r := range list {
		if subs[i] == subID && r.GroupKey != group {
			busy = append(busy, T(ctx, "“%s” su %s", r.Title, r.Channel))
		}
	}
	if len(busy) < max {
		return "", nil
	}
	if max == 1 {
		return T(ctx, "l'abbonamento ha %d connessione e a quell'ora registra già %s", max, strings.Join(busy, ", ")), nil
	}
	return T(ctx, "l'abbonamento ha %d connessioni e a quell'ora registra già %s", max, strings.Join(busy, ", ")), nil
}
