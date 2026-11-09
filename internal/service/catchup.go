package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/silirim/vodmax/internal/playlist/xtream"
)

// Catch-up: channels whose provider keeps an archive (tv_archive) can be
// watched from a past moment, within their archive days. The archive is
// played like a film (remux to fragmented MP4, seekable by asking the
// archive from another minute). Times in archive URLs are in the
// provider server's time zone, read at login and kept in memory.

type catchupInfo struct {
	client   *xtream.Client
	streamID string
	start    time.Time
	stop     time.Time
	loc      *time.Location
}

type catchupState struct {
	mu   sync.Mutex
	zone map[int64]*time.Location // by subscription
}

const maxCatchup = 6 * time.Hour

func (s *Service) serverZone(ctx context.Context, subID int64, c *xtream.Client) (*time.Location, error) {
	s.cu.mu.Lock()
	loc := s.cu.zone[subID]
	s.cu.mu.Unlock()
	if loc != nil {
		return loc, nil
	}
	_, info, err := c.LoginInfo(ctx)
	if err != nil {
		return nil, err
	}
	loc = info.Location()
	s.cu.mu.Lock()
	if s.cu.zone == nil {
		s.cu.zone = map[int64]*time.Location{}
	}
	s.cu.zone[subID] = loc
	s.cu.mu.Unlock()
	return loc, nil
}

// NewCatchupSession prepares the archive of a channel between start and
// stop (a guide programme, or "from the start" of the one on air).
func (s *Service) NewCatchupSession(ctx context.Context, channelID int64, start, stop time.Time, viewer, title string) (PlaybackSession, error) {
	if !viewerID.MatchString(viewer) {
		return PlaybackSession{}, invalid("identificativo del player non valido")
	}
	it, err := s.st.PlaybackItem(ctx, channelID)
	if err != nil {
		return PlaybackSession{}, err
	}
	if it.Kind != "live" {
		return PlaybackSession{}, invalid("non è un canale")
	}
	if err := s.allowItem(ctx, channelID); err != nil {
		return PlaybackSession{}, err
	}
	variant, days, err := s.st.CatchupVariant(ctx, channelID)
	if err != nil {
		return PlaybackSession{}, err
	}
	if days > 0 {
		it = variant // the quality whose archive the provider keeps
	}
	now := time.Now()
	switch {
	case days <= 0:
		return PlaybackSession{}, invalid("questo canale non ha l'archivio del provider")
	case !stop.After(start) || stop.Sub(start) > maxCatchup:
		return PlaybackSession{}, invalid("intervallo non valido (massimo 6 ore)")
	case start.Before(now.AddDate(0, 0, -days)):
		return PlaybackSession{}, invalid("l'archivio di questo canale copre solo %d giorni", days)
	case !start.Before(now.Add(-time.Minute)):
		return PlaybackSession{}, invalid("il programma non è ancora iniziato")
	}
	// On air: what the archive has so far. The programme's stop is kept,
	// so a later opening (seek, or going on at the end) asks for more.
	end := stop
	if end.After(now) {
		end = now
	}
	s.stopViewerTimeshift(viewer)
	client, err := s.xtreamClient(ctx, it.SubscriptionID, time.Minute)
	if err != nil {
		return PlaybackSession{}, err
	}
	loc, err := s.serverZone(ctx, it.SubscriptionID, client)
	if err != nil {
		return PlaybackSession{}, err
	}
	if title == "" {
		title = it.Title
	}
	title = fmt.Sprintf("%s · %s %s", title, it.Title, start.Format("02/01 15:04"))
	sess := &vodSession{id: randomToken(), viewer: viewer, media: "catchup", mediaID: channelID, subID: it.SubscriptionID,
		title: title, mode: "remux", internal: randomToken(), expires: now.Add(8 * time.Hour),
		catchup: &catchupInfo{client: client, streamID: it.SourceID, start: start, stop: stop, loc: loc},
		// No analysis: the archive is the channel's MPEG-TS (H.264 video
		// is copied, audio converted to AAC).
		probe: &probeResult{Duration: end.Sub(start).Seconds(), VideoCodec: "h264", Audio: []Track{}, Subtitles: []Track{}}}
	s.addVODSession(sess)
	return PlaybackSession{ID: sess.id, Mode: "remux", Title: title, Duration: int(end.Sub(start).Seconds()),
		URL: "/stream/vod/" + sess.id + "/remux", Audio: []Track{}, Subtitles: []Track{}, TS: s.pacedRemux()}, nil
}
