package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Schedule says when catalogs and guides are refreshed automatically.
type Schedule struct {
	CatalogHour int `json:"catalog_hour"` // local hour of the nightly catalog sync, -1 = off
	GuideHours  int `json:"guide_hours"`  // guide refresh interval in hours, 0 = off
}

var defaultSchedule = Schedule{CatalogHour: 4, GuideHours: 6}

const (
	scheduleKey     = "schedule"
	maintenanceTick = time.Minute
	vpnCheckTick    = 20 * time.Second
	// Without a handshake for this long a tunnel is restarted (endpoint
	// resolved again, new handshake). The keepalive is 25 s.
	vpnStaleAfter = 75 * time.Second
	vpnMaxBackoff = 5 * time.Minute
	syncRetry     = time.Hour // after a failed nightly sync
)

// VPNHealth is what the watchdog knows about a connection.
type VPNHealth struct {
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	Since      time.Time `json:"since"` // of the current state
	Reconnects int       `json:"reconnects"`
}

type vpnHealth struct {
	VPNHealth
	stale time.Time // first check without a fresh handshake
	fails int
	next  time.Time // no new attempt before
}

type maintenanceState struct {
	// Guarded by Service.mu.
	vpn        map[int64]*vpnHealth
	guideErr   map[int64]string
	guideRetry map[int64]time.Time
	backupDir  string
	rulesRun   time.Time
}

func (s *Service) Schedule(ctx context.Context) (Schedule, error) {
	sc := defaultSchedule
	err := s.st.Setting(ctx, scheduleKey, &sc)
	return sc, err
}

func (s *Service) SetSchedule(ctx context.Context, sc Schedule) error {
	if sc.CatalogHour < -1 || sc.CatalogHour > 23 {
		return invalid("ora della sincronizzazione del catalogo non valida (0-23, oppure -1 per disattivarla)")
	}
	if sc.GuideHours < 0 || sc.GuideHours > 48 {
		return invalid("intervallo della guida non valido (1-48 ore, oppure 0 per disattivarlo)")
	}
	return s.st.SetSetting(ctx, scheduleKey, sc)
}

// StartMaintenance runs, until ctx ends, the VPN watchdog and the scheduled
// catalog and guide refreshes.
func (s *Service) StartMaintenance(ctx context.Context, log *slog.Logger) {
	go func() {
		t := time.NewTicker(vpnCheckTick)
		defer t.Stop()
		for {
			s.checkVPNs(ctx, log)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	go func() {
		// The people index, at night (people.go).
		for {
			if inPeopleNight(time.Now()) {
				s.FillPeople(ctx, log, func() bool { return inPeopleNight(time.Now()) })
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Minute):
			}
		}
	}()
	go func() {
		t := time.NewTicker(maintenanceTick)
		defer t.Stop()
		for {
			s.runScheduled(ctx, log, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// checkVPNs keeps up the connections used by subscriptions: a tunnel that
// is down, or that has had no handshake for vpnStaleAfter, is restarted
// with an increasing pause between failed attempts.
func (s *Service) checkVPNs(ctx context.Context, log *slog.Logger) {
	subs, err := s.st.Subscriptions(ctx)
	if err != nil {
		return
	}
	used := map[int64]bool{}
	for _, sub := range subs {
		if sub.VPNID != 0 { // collections (folders) need no VPN
			used[sub.VPNID] = true
		}
	}
	now := time.Now()
	for id := range used {
		s.mu.Lock()
		off := s.vpnOff[id]
		h := s.maint.vpn[id]
		if h == nil {
			h = &vpnHealth{VPNHealth: VPNHealth{Since: now}}
			s.maint.vpn[id] = h
		}
		t := s.tunnels[id]
		s.mu.Unlock()
		if off {
			s.setVPNHealth(h, false, "scollegata manualmente", now)
			continue
		}
		if t != nil && t.Up() {
			if t.Status().Connected {
				s.mu.Lock()
				h.stale, h.fails = time.Time{}, 0
				s.mu.Unlock()
				s.setVPNHealth(h, true, "", now)
				continue
			}
			s.mu.Lock()
			if h.stale.IsZero() {
				h.stale = now
			}
			waited := now.Sub(h.stale)
			s.mu.Unlock()
			if waited < vpnStaleAfter {
				continue
			}
			s.setVPNHealth(h, false, "nessuna risposta dal server VPN da "+waited.Round(time.Second).String(), now)
			s.closeTunnel(id)
		}
		s.mu.Lock()
		wait := now.Before(h.next)
		s.mu.Unlock()
		if wait {
			continue
		}
		start, cancel := context.WithTimeout(ctx, 30*time.Second)
		nt, err := s.tunnel(start, id)
		cancel()
		s.mu.Lock()
		if t != nil {
			h.Reconnects++ // a tunnel existed: this is a reconnection
		}
		h.stale = time.Time{}
		if err != nil {
			h.fails++
			backoff := min(vpnMaxBackoff, vpnCheckTick<<min(h.fails, 4))
			h.next = now.Add(backoff)
		} else {
			h.stale = now // a handshake is expected within vpnStaleAfter
		}
		s.mu.Unlock()
		if err != nil {
			s.setVPNHealth(h, false, "collegamento non riuscito: "+err.Error(), now)
			log.Warn("VPN non collegata", "vpn", id, "err", err)
		} else {
			if nt.Status().Connected { // a VPN outside VodMax, or an immediate handshake
				s.setVPNHealth(h, true, "", now)
			}
			log.Info("VPN (ri)collegata", "vpn", id)
		}
	}
}

func (s *Service) setVPNHealth(h *vpnHealth, ok bool, msg string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h.OK != ok || h.Since.IsZero() {
		h.Since = now
	}
	h.OK, h.Error = ok, msg
}

// runScheduled starts what is due: the nightly catalog sync (which also
// refreshes the guide) and, in between, the guide alone.
func (s *Service) runScheduled(ctx context.Context, log *slog.Logger, now time.Time) {
	sc, err := s.Schedule(ctx)
	if err != nil {
		return
	}
	s.updateDue(ctx, log, now)
	if s.autoBackupDue() {
		if _, err := s.AutoBackup(ctx); err != nil && !errors.Is(err, errNoBackupDir) {
			log.Warn("backup automatico non riuscito", "err", err)
		}
	}
	subs, err := s.st.Subscriptions(ctx)
	if err != nil {
		return
	}
	providerGuide := false
	for _, sub := range subs {
		if ctx.Err() != nil {
			return
		}
		if catalogDue(sc, sub.SyncedAt, sub.SyncStatus, now) {
			res, err := s.Sync(ctx, sub.ID, false)
			if err != nil {
				log.Warn("sincronizzazione programmata fallita", "abbonamento", sub.ID, "err", err)
			} else {
				log.Info("sincronizzazione programmata", "abbonamento", sub.ID, "durata_ms", int64(res.Duration))
			}
			continue
		}
		if sub.Kind != "xtream" || sc.GuideHours == 0 {
			continue
		}
		var last time.Time
		s.st.Setting(ctx, guideKey(sub.ID), &last)
		s.mu.Lock()
		retry := s.maint.guideRetry[sub.ID]
		s.mu.Unlock()
		if now.Sub(last) < time.Duration(sc.GuideHours)*time.Hour || now.Before(retry) {
			continue
		}
		_, err := s.SyncGuide(ctx, sub.ID)
		s.guideDone(ctx, sub.ID, err)
		if err != nil {
			log.Warn("aggiornamento della guida fallito", "abbonamento", sub.ID, "err", err)
		} else {
			providerGuide = true
		}
	}
	if providerGuide {
		s.syncExternalGuides(ctx) // they extend the channels it replaced
		s.applyRules(ctx, log)
		s.cleanupRecordings(ctx, log)
		return
	}
	defer func() {
		s.mu.Lock()
		due := time.Since(s.maint.rulesRun) >= rulesEvery
		s.mu.Unlock()
		if due {
			s.applyRules(ctx, log)
		}
		s.cleanupRecordings(ctx, log)
	}()
	// External guides after the providers', which they complete.
	if sc.GuideHours == 0 {
		return
	}
	sources, err := s.st.GuideSources(ctx)
	if err != nil {
		return
	}
	for _, g := range sources {
		if !g.Enabled || ctx.Err() != nil {
			continue
		}
		every := time.Duration(sc.GuideHours) * time.Hour
		if g.Status == "error" {
			every = time.Hour
		}
		if g.SyncedAt != nil && now.Sub(time.Unix(*g.SyncedAt, 0)) < every {
			continue
		}
		if _, err := s.SyncGuideSource(ctx, g.ID); err != nil {
			log.Warn("guida esterna non aggiornata", "guida", g.Name, "err", err)
		}
	}
}

// catalogDue: never synced, the nightly hour has passed since the last
// sync, or the last sync failed more than syncRetry ago.
func catalogDue(sc Schedule, synced *time.Time, status string, now time.Time) bool {
	if sc.CatalogHour < 0 {
		return false
	}
	if synced == nil {
		return true
	}
	if status == "error" && now.Sub(*synced) >= syncRetry {
		return true
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), sc.CatalogHour, 0, 0, 0, now.Location())
	if slot.After(now) {
		slot = slot.AddDate(0, 0, -1)
	}
	return synced.Before(slot)
}

func guideKey(subID int64) string { return fmt.Sprintf("guide_synced:%d", subID) }

// guideDone records a guide refresh: the time on success; after a
// failure the error is shown and the next attempt waits an hour.
func (s *Service) guideDone(ctx context.Context, subID int64, err error) {
	s.mu.Lock()
	if err != nil {
		s.maint.guideErr[subID] = err.Error()
		s.maint.guideRetry[subID] = time.Now().Add(time.Hour)
	} else {
		delete(s.maint.guideErr, subID)
		delete(s.maint.guideRetry, subID)
	}
	s.mu.Unlock()
	if err == nil {
		s.st.SetSetting(ctx, guideKey(subID), time.Now())
	}
}

// Problem is something the user should know about.
type Problem struct {
	Level string `json:"level"` // "error" or "warning"
	Area  string `json:"area"`  // "vpn", "subscription", "guide", "profile", "disk"
	Text  string `json:"text"`
}

// SystemStatus is shown as a warning in the interface.
type SystemStatus struct {
	Problems    []Problem  `json:"problems"`
	Schedule    Schedule   `json:"schedule"`
	NextCatalog *time.Time `json:"next_catalog,omitempty"`
}

const expiryWarning = 15 * 24 * time.Hour

func (s *Service) Status(ctx context.Context) (SystemStatus, error) {
	out := SystemStatus{Problems: []Problem{}}
	sc, err := s.Schedule(ctx)
	if err != nil {
		return out, err
	}
	out.Schedule = sc
	if sc.CatalogHour >= 0 {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), sc.CatalogHour, 0, 0, 0, now.Location())
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		out.NextCatalog = &next
	}
	subs, err := s.st.Subscriptions(ctx)
	if err != nil {
		return out, err
	}
	vpns, err := s.st.VPNs(ctx)
	if err != nil {
		return out, err
	}
	vpnName := map[int64]string{}
	for _, v := range vpns {
		vpnName[v.ID] = v.Name
	}
	s.mu.Lock()
	for id, h := range s.maint.vpn {
		if h.OK || vpnName[id] == "" || h.Error == "" { // "" : not checked yet
			continue
		}
		out.Problems = append(out.Problems, Problem{Level: "error", Area: "vpn",
			Text: T(ctx, "VPN “%s” non funziona (%s): gli abbonamenti che la usano non sono raggiungibili", vpnName[id], h.Error)})
	}
	guideErr := map[int64]string{}
	for id, e := range s.maint.guideErr {
		guideErr[id] = e
	}
	s.mu.Unlock()
	out.Problems = append(out.Problems, s.diskProblems(ctx)...)
	// An admin profile without PIN, with other profiles, can be chosen by
	// anyone (it has no admin rights until the PIN is set); with a kids
	// profile, so can an adult one.
	if profiles, err := s.st.Profiles(ctx); err == nil {
		kids := false
		for _, p := range profiles {
			kids = kids || p.Kind == "kids"
		}
		for _, p := range profiles {
			if p.Kind == "admin" && !p.HasPIN && len(profiles) > 1 {
				out.Problems = append(out.Problems, Problem{Level: "error", Area: "profile",
					Text: T(ctx, "Il profilo amministratore “%s” non ha un PIN: entrandoci si chiede di impostarlo", p.Name)})
			} else if kids && p.Kind != "kids" && !p.HasPIN {
				out.Problems = append(out.Problems, Problem{Level: "warning", Area: "profile",
					Text: T(ctx, "Il profilo “%s” non ha un PIN: dai dispositivi dei bambini si può sceglierlo", p.Name)})
			}
		}
	}
	if sources, err := s.st.GuideSources(ctx); err == nil {
		for _, g := range sources {
			if g.Enabled && g.Status == "error" {
				out.Problems = append(out.Problems, Problem{Level: "warning", Area: "guide",
					Text: T(ctx, "Guida esterna “%s” non aggiornata: %s", g.Name, g.Error)})
			}
		}
	}
	for _, sub := range subs {
		if sub.SyncStatus == "error" {
			out.Problems = append(out.Problems, Problem{Level: "error", Area: "subscription",
				Text: T(ctx, "Ultima sincronizzazione di “%s” non riuscita: %s", sub.Name, TranslateText(LangOf(ctx), sub.SyncError))})
		}
		var acc struct {
			Active  *bool `json:"active"`
			Expires int64 `json:"expires"`
		}
		json.Unmarshal(sub.Account, &acc)
		if acc.Active != nil && !*acc.Active {
			out.Problems = append(out.Problems, Problem{Level: "error", Area: "subscription",
				Text: T(ctx, "L'account di “%s” non è attivo presso il provider", sub.Name)})
		} else if acc.Expires > 0 {
			if left := time.Until(time.Unix(acc.Expires, 0)); left < expiryWarning {
				out.Problems = append(out.Problems, Problem{Level: "warning", Area: "subscription",
					Text: T(ctx, "L'abbonamento “%s” scade il %s", sub.Name, time.Unix(acc.Expires, 0).Format("02/01/2006"))})
			}
		}
		if e := guideErr[sub.ID]; e != "" {
			out.Problems = append(out.Problems, Problem{Level: "warning", Area: "guide",
				Text: T(ctx, "Guida di “%s” non aggiornata: %s", sub.Name, e)})
		}
	}
	return out, nil
}
