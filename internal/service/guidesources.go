package service

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/silirim/vodmax/internal/epg"
	"github.com/silirim/vodmax/internal/lineup"
	"github.com/silirim/vodmax/internal/store"
)

// External XMLTV guides (an EPG site, the home Tvheadend...) fill in the
// channels the provider's guide leaves without programmes. They are not
// the playlist provider: they are fetched directly, not through the VPN. A
// source only takes channels that no other source has.

func extSource(id int64) string { return fmt.Sprintf("ext:%d", id) }

func checkGuideURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", invalid("indirizzo della guida non valido (atteso http:// o https://, anche .xml.gz)")
	}
	return u.String(), nil
}

// GuideSources lists the sources with any password or key in their
// addresses hidden (hideURLSecrets); a patch that sends it back keeps the stored one.
func (s *Service) GuideSources(ctx context.Context) ([]store.GuideSource, error) {
	list, err := s.st.GuideSources(ctx)
	for i := range list {
		list[i].URL = hideURLSecrets(list[i].URL)
		list[i].Error = TranslateText(LangOf(ctx), list[i].Error)
	}
	return list, err
}

// secretParam tells query parameters that usually carry a key or a password.
var secretParam = regexp.MustCompile(`(?i)(key|token|pass|pwd|secret|auth|sig|user)`)

// hideURLSecrets hides the password and the values of key-like parameters.
func hideURLSecrets(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if _, has := u.User.Password(); has {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			if secretParam.MatchString(k) {
				q[k] = []string{"***"}
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func (s *Service) AddGuideSource(ctx context.Context, name, rawURL string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return 0, invalid("nome della guida obbligatorio (max 60 caratteri)")
	}
	u, err := checkGuideURL(rawURL)
	if err != nil {
		return 0, err
	}
	return s.st.AddGuideSource(ctx, name, u)
}

// GuideSourcePatch changes a source; nil fields are kept.
type GuideSourcePatch struct {
	Name    *string `json:"name"`
	URL     *string `json:"url"`
	Enabled *bool   `json:"enabled"`
}

func (s *Service) UpdateGuideSource(ctx context.Context, id int64, p GuideSourcePatch) error {
	g, err := s.st.GuideSource(ctx, id)
	if err != nil {
		return err
	}
	if p.Name != nil {
		g.Name = strings.TrimSpace(*p.Name)
		if g.Name == "" || utf8.RuneCountInString(g.Name) > 60 {
			return invalid("nome della guida obbligatorio (max 60 caratteri)")
		}
	}
	if p.URL != nil && *p.URL != hideURLSecrets(g.URL) {
		if g.URL, err = checkGuideURL(*p.URL); err != nil {
			return err
		}
	}
	if p.Enabled != nil {
		g.Enabled = *p.Enabled
		if !g.Enabled {
			if err := s.st.DeleteGuide(ctx, extSource(id)); err != nil {
				return err
			}
		}
	}
	return s.st.UpdateGuideSource(ctx, g)
}

func (s *Service) DeleteGuideSource(ctx context.Context, id int64) error {
	return s.st.DeleteGuideSource(ctx, id)
}

// SyncGuideSource downloads an external guide and keeps the programmes of
// the visible channels that have none from other sources.
func (s *Service) SyncGuideSource(ctx context.Context, id int64) (GuideResult, error) {
	g, err := s.st.GuideSource(ctx, id)
	if err != nil {
		return GuideResult{}, err
	}
	res, err := s.syncGuideSource(ctx, g)
	status, msg := "ok", ""
	if err != nil {
		status, msg = "error", err.Error()
	}
	s.st.SetGuideSourceResult(context.WithoutCancel(ctx), id, status, msg, res.Channels, res.Programmes)
	return res, err
}

func (s *Service) syncGuideSource(ctx context.Context, g store.GuideSource) (GuideResult, error) {
	started := time.Now()
	source := extSource(g.ID)
	cover, taken, err := s.st.GuideCoverage(ctx, source, time.Now())
	if err != nil {
		return GuideResult{}, err
	}
	byID, byName := map[string]*store.GuideCover{}, map[string]*store.GuideCover{}
	for i := range cover {
		c := &cover[i]
		if c.EPGID != "" {
			byID[c.EPGID] = c
		}
		if c.NameKey != "" {
			byName[c.NameKey] = c
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.URL, nil)
	if err != nil {
		return GuideResult{}, invalid("indirizzo non valido")
	}
	req.Header.Set("User-Agent", "VodMax")
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return GuideResult{}, fmt.Errorf("guida non raggiungibile: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return GuideResult{}, fmt.Errorf("guida: risposta HTTP %d", resp.StatusCode)
	}
	// Plain or gzip, whatever the name or headers say.
	body := bufio.NewReaderSize(io.LimitReader(resp.Body, 1<<30), 64<<10)
	var r io.Reader = body
	if magic, _ := body.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(body)
		if err != nil {
			return GuideResult{}, fmt.Errorf("guida compressa illeggibile: %w", err)
		}
		r = io.LimitReader(zr, 2<<30)
	}
	from, to := time.Now().Add(-guidePast), time.Now().Add(guideFuture)
	// A channel without programmes gets this source's channel; a covered
	// one gets its programmes after the last it has (more days).
	own := map[string]bool{}
	extend := map[string]*store.GuideCover{}
	var channels []store.GuideChannel
	guide, err := epg.Parse(r, epg.Options{
		OnChannel: func(c epg.Channel) {
			match := byID[c.ID]
			key := ""
			for _, n := range c.Names {
				if k := lineup.Normalize(n); byName[k] != nil {
					key = k
					if match == nil {
						match = byName[k]
					}
					break
				}
			}
			if match == nil {
				return
			}
			if match.LastStop > 0 {
				extend[c.ID] = match
				return
			}
			if taken[c.ID] {
				return
			}
			if key == "" && len(c.Names) > 0 {
				key = lineup.Normalize(c.Names[0])
			}
			channels = append(channels, store.GuideChannel{ID: c.ID, NameKey: key})
			own[c.ID] = true
		},
		Keep: func(ch string, start, stop time.Time) bool {
			if own[ch] {
				return stop.After(from) && start.Before(to)
			}
			if c := extend[ch]; c != nil {
				return start.Unix() >= c.LastStop && start.Before(to)
			}
			return false
		},
	})
	if err != nil {
		return GuideResult{}, fmt.Errorf("guida illeggibile: %w", err)
	}
	programmes := make([]store.GuideProgramme, 0, len(guide.Programmes))
	extended := map[string]bool{}
	for _, p := range guide.Programmes {
		ch := p.Channel
		if c := extend[ch]; c != nil {
			ch = c.Guide
			extended[ch] = true
		}
		programmes = append(programmes, store.GuideProgramme{Channel: ch, Start: p.Start.Unix(), Stop: p.Stop.Unix(),
			Title: p.Title, Subtitle: p.Subtitle, Description: p.Description, Category: p.Category, Airing: p.Airing})
	}
	if err := s.st.ReplaceGuide(ctx, source, channels, programmes); err != nil {
		return GuideResult{}, err
	}
	return GuideResult{Channels: len(channels) + len(extended), Programmes: len(programmes), Duration: time.Since(started) / time.Millisecond}, nil
}

// syncExternalGuides refreshes the enabled external guides (after a
// provider guide, which replaces the extensions of its channels).
func (s *Service) syncExternalGuides(ctx context.Context) {
	sources, err := s.st.GuideSources(ctx)
	if err != nil {
		return
	}
	for _, g := range sources {
		if g.Enabled {
			s.SyncGuideSource(ctx, g.ID)
		}
	}
}

// unwrapURLError drops the URL (it may carry a token) from client errors.
func unwrapURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}

// SyncProviderGuide refreshes a subscription's guide, then the external
// guides that extend it.
func (s *Service) SyncProviderGuide(ctx context.Context, subID int64) (GuideResult, error) {
	res, err := s.SyncGuide(ctx, subID)
	s.guideDone(ctx, subID, err)
	if err == nil {
		s.syncExternalGuides(ctx)
	}
	return res, err
}
