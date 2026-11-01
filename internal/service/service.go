// Package service ties together storage, encrypted secrets, the internal
// VPN tunnels and catalog synchronisation.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/silirim/vodmax/internal/catalog"
	"github.com/silirim/vodmax/internal/playlist/m3u"
	"github.com/silirim/vodmax/internal/playlist/stalker"
	"github.com/silirim/vodmax/internal/playlist/xtream"
	"github.com/silirim/vodmax/internal/provider"
	"github.com/silirim/vodmax/internal/secret"
	"github.com/silirim/vodmax/internal/store"
	"github.com/silirim/vodmax/internal/vpn"
)

// Tunnel is the part of *vpn.Tunnel the service needs.
type Tunnel interface {
	provider.Dialer
	Status() vpn.Status
	Close()
}

// StartTunnel brings a tunnel up; replaced in tests.
type StartTunnel func(ctx context.Context, cfg *vpn.Config) (Tunnel, error)

// Service is safe for concurrent use.
type Service struct {
	st    *store.Store
	box   *secret.Box
	start StartTunnel

	mu      sync.Mutex
	tunnels map[int64]Tunnel
	syncing map[int64]bool
	vpnOff  map[int64]bool // disconnected by the user

	selMu  sync.Mutex // serialises selection recomputation
	live   liveState
	conns  connManager
	images imageState
	vod    vodState
	rec    recorderState
	maint  maintenanceState
	auth   authState
	meta   metaState
	trend  trendingCache
	ts     timeshiftState
	cu     catchupState
	upd    updateState
	dl     downloadState
	sso    ssoState
	asst   assistantState

	stalkerTokens sync.Map // subscription id → portal session
}

func New(st *store.Store, box *secret.Box, start StartTunnel) *Service {
	if start == nil {
		start = func(ctx context.Context, cfg *vpn.Config) (Tunnel, error) { return vpn.Start(ctx, cfg, nil) }
	}
	return &Service{st: st, box: box, start: start, tunnels: map[int64]Tunnel{}, syncing: map[int64]bool{}, vpnOff: map[int64]bool{},
		maint: maintenanceState{vpn: map[int64]*vpnHealth{}, guideErr: map[int64]string{}, guideRetry: map[int64]time.Time{}}}
}

// Close shuts every tunnel down.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, t := range s.tunnels {
		t.Close()
		delete(s.tunnels, id)
	}
}

// ErrInvalid wraps user input errors (HTTP 400). The format and its
// arguments are kept to show the message in another language.
type ErrInvalid struct {
	Msg    string
	format string
	args   []any
}

func (e ErrInvalid) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ErrInvalid{Msg: fmt.Sprintf(format, args...), format: format, args: args}
}

// Invalid is invalid for other packages (the API).
func Invalid(format string, args ...any) error { return invalid(format, args...) }

// ---- VPN ----

// VPNView is a VPN connection as shown to the user.
type VPNView struct {
	store.VPN
	Endpoint string     `json:"endpoint"`
	Running  bool       `json:"running"`
	Status   vpn.Status `json:"status"`
	Health   *VPNHealth `json:"health,omitempty"`
}

func vpnOwner(id int64) string { return fmt.Sprintf("vpn:%d", id) }

// AddVPN validates a wg-quick configuration and stores it encrypted.
func (s *Service) AddVPN(ctx context.Context, name, conf string) (int64, error) {
	return s.AddVPNKind(ctx, name, "wireguard", conf)
}

// AddVPNKind adds a connection: "wireguard" (an internal tunnel from a
// wg-quick configuration) or "system" (the user runs a VPN outside
// VodMax, on the device or the router: VodMax uses the normal network and
// cannot check that it really goes through a VPN).
func (s *Service) AddVPNKind(ctx context.Context, name, kind, conf string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return 0, invalid("nome della VPN obbligatorio (max 80 caratteri)")
	}
	switch kind {
	case "", "wireguard":
		kind = "wireguard"
		if _, err := vpn.ParseConfig(strings.NewReader(conf)); err != nil {
			return 0, invalid("configurazione WireGuard non valida: %v", err)
		}
	case "system":
		conf = ""
	default:
		return 0, invalid("tipo di VPN non valido: wireguard o system")
	}
	return s.st.AddVPN(ctx, name, kind, func(id int64) []byte { return s.box.Seal([]byte(conf), vpnOwner(id)) })
}

func (s *Service) vpnConfig(ctx context.Context, id int64) (*vpn.Config, error) {
	v, err := s.st.VPN(ctx, id)
	if err != nil {
		return nil, err
	}
	raw, err := s.box.Open(v.Config, vpnOwner(id))
	if err != nil {
		return nil, err
	}
	return vpn.ParseConfig(bytes.NewReader(raw))
}

func (s *Service) VPNs(ctx context.Context) ([]VPNView, error) {
	list, err := s.st.VPNs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]VPNView, 0, len(list))
	for _, v := range list {
		view := VPNView{VPN: v}
		if v.Kind == "system" {
			view.Endpoint = "VPN esterna (rete del sistema)"
		} else if cfg, err := s.vpnConfig(ctx, v.ID); err == nil {
			view.Endpoint = cfg.Endpoint
		}
		s.mu.Lock()
		if h := s.maint.vpn[v.ID]; h != nil {
			c := h.VPNHealth
			view.Health = &c
		}
		if t := s.tunnels[v.ID]; t != nil && t.Up() {
			view.Running, view.Status = true, t.Status()
		}
		s.mu.Unlock()
		out = append(out, view)
	}
	return out, nil
}

// ConnectVPN starts (or returns) the tunnel and waits for a handshake.
func (s *Service) ConnectVPN(ctx context.Context, id int64) (vpn.Status, error) {
	s.mu.Lock()
	delete(s.vpnOff, id)
	s.mu.Unlock()
	t, err := s.tunnel(ctx, id)
	if err != nil {
		return vpn.Status{}, err
	}
	for {
		if st := t.Status(); st.Connected {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return t.Status(), errors.New("nessun handshake con il server VPN")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// DisconnectVPN closes the tunnel at the user's request; subscriptions
// using it stop at once and the watchdog leaves it down until the user
// connects it again (or a subscription needs it).
func (s *Service) DisconnectVPN(id int64) {
	s.mu.Lock()
	s.vpnOff[id] = true
	s.mu.Unlock()
	s.closeTunnel(id)
}

func (s *Service) closeTunnel(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tunnels[id]; t != nil {
		t.Close()
		delete(s.tunnels, id)
	}
}

func (s *Service) DeleteVPN(ctx context.Context, id int64) error {
	if err := s.st.DeleteVPN(ctx, id); err != nil {
		return err
	}
	s.closeTunnel(id)
	return nil
}

func (s *Service) tunnel(ctx context.Context, id int64) (Tunnel, error) {
	s.mu.Lock()
	t := s.tunnels[id]
	s.mu.Unlock()
	if t != nil && t.Up() {
		return t, nil
	}
	if v, err := s.st.VPN(ctx, id); err != nil {
		return nil, err
	} else if v.Kind == "system" {
		t = systemNetwork{}
	} else {
		cfg, err := s.vpnConfig(ctx, id)
		if err != nil {
			return nil, err
		}
		if t, err = s.start(ctx, cfg); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.tunnels[id]; old != nil && old.Up() {
		t.Close() // another caller won the race
		return old, nil
	}
	s.tunnels[id] = t
	return t, nil
}

// ---- Subscriptions ----

// SubscriptionInput is what the user submits. Password and URL are secrets.
type SubscriptionInput struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	VPNID    int64  `json:"vpn_id"`
	Server   string `json:"server,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	URL      string `json:"url,omitempty"`
	MAC      string `json:"mac,omitempty"`
	// Local collection: the folder and what it holds (movie or series).
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
}

type xtreamSettings struct {
	Server   string `json:"server"`
	Username string `json:"username"`
}

type subscriptionSecret struct {
	Password string `json:"password,omitempty"`
	URL      string `json:"url,omitempty"`
	MAC      string `json:"mac,omitempty"` // Stalker portal
}

func subOwner(id int64) string { return fmt.Sprintf("subscription:%d", id) }

func (s *Service) AddSubscription(ctx context.Context, in SubscriptionInput) (int64, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 {
		return 0, invalid("nome dell'abbonamento obbligatorio (max 80 caratteri)")
	}
	if in.Kind == "local" {
		set, err := checkLocal(in.Path, in.Content)
		if err != nil {
			return 0, err
		}
		rawSettings, _ := json.Marshal(set)
		return s.st.AddSubscription(ctx, store.Subscription{Name: in.Name, Kind: "local", Settings: rawSettings},
			func(id int64) []byte { return s.box.Seal([]byte("{}"), subOwner(id)) })
	}
	if _, err := s.st.VPN(ctx, in.VPNID); err != nil {
		return 0, invalid("ogni abbonamento richiede una connessione VPN esistente")
	}
	var settings any
	var sec subscriptionSecret
	switch in.Kind {
	case "xtream":
		if err := noCredentialsIn(in.Server); err != nil {
			return 0, err
		}
		if _, err := xtream.New(in.Server, in.Username, in.Password, nil); err != nil {
			return 0, invalid("%v", err)
		}
		settings = xtreamSettings{Server: strings.TrimSpace(in.Server), Username: in.Username}
		sec.Password = in.Password
	case "m3u":
		u, err := url.Parse(strings.TrimSpace(in.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return 0, invalid("URL della playlist non valido (atteso http:// o https://)")
		}
		settings = map[string]string{"host": u.Hostname()}
		sec.URL = u.String()
	case "stalker":
		mac, err := stalker.NormalizeMAC(in.MAC)
		if err == nil {
			err = noCredentialsIn(in.Server)
		}
		if err == nil {
			_, err = stalker.New(in.Server, mac, nil)
		}
		if err != nil {
			return 0, invalid("%v", err)
		}
		settings = xtreamSettings{Server: strings.TrimSpace(in.Server)}
		sec.MAC = mac
	default:
		return 0, invalid("tipo di abbonamento non valido: usa xtream, m3u o stalker")
	}
	rawSettings, _ := json.Marshal(settings)
	rawSecret, _ := json.Marshal(sec)
	return s.st.AddSubscription(ctx, store.Subscription{Name: in.Name, Kind: in.Kind, VPNID: in.VPNID, Settings: rawSettings},
		func(id int64) []byte { return s.box.Seal(rawSecret, subOwner(id)) })
}

// SubscriptionPatch changes an existing subscription; nil fields are kept.
// Secrets are never returned: an empty password or URL keeps the stored one.
type SubscriptionPatch struct {
	Name     *string `json:"name"`
	VPNID    *int64  `json:"vpn_id"`
	Server   *string `json:"server"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	URL      *string `json:"url"`
	MAC      *string `json:"mac"`
	Path     *string `json:"path"`
	Content  *string `json:"content"`
}

func (s *Service) UpdateSubscription(ctx context.Context, id int64, p SubscriptionPatch) error {
	sub, err := s.st.Subscription(ctx, id)
	if err != nil {
		return err
	}
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" || utf8.RuneCountInString(n) > 80 {
			return invalid("nome dell'abbonamento obbligatorio (max 80 caratteri)")
		}
		p.Name = &n
	}
	if sub.Kind == "local" {
		var set localSettings
		json.Unmarshal(sub.Settings, &set)
		if p.Path != nil {
			set.Path = *p.Path
		}
		if p.Content != nil {
			set.Content = *p.Content
		}
		set, err := checkLocal(set.Path, set.Content)
		if err != nil {
			return err
		}
		settings, _ := json.Marshal(set)
		return s.st.UpdateSubscription(ctx, id, p.Name, nil, settings, nil)
	}
	if p.VPNID != nil {
		if _, err := s.st.VPN(ctx, *p.VPNID); err != nil {
			return invalid("ogni abbonamento richiede una connessione VPN esistente")
		}
	}
	raw, err := s.box.Open(sub.Secret, subOwner(id))
	if err != nil {
		return err
	}
	var sec subscriptionSecret
	json.Unmarshal(raw, &sec)
	var settings json.RawMessage
	var secret []byte
	switch sub.Kind {
	case "xtream":
		var set xtreamSettings
		json.Unmarshal(sub.Settings, &set)
		if p.Server != nil {
			set.Server = strings.TrimSpace(*p.Server)
		}
		if p.Username != nil {
			set.Username = *p.Username
		}
		if p.Password != nil && *p.Password != "" {
			sec.Password = *p.Password
		}
		if p.Server != nil {
			if err := noCredentialsIn(set.Server); err != nil {
				return err
			}
		}
		if _, err := xtream.New(set.Server, set.Username, sec.Password, nil); err != nil {
			return invalid("%v", err)
		}
		settings, _ = json.Marshal(set)
	case "m3u":
		if p.URL != nil && *p.URL != "" {
			u, err := url.Parse(strings.TrimSpace(*p.URL))
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return invalid("URL della playlist non valido (atteso http:// o https://)")
			}
			sec.URL = u.String()
			settings, _ = json.Marshal(map[string]string{"host": u.Hostname()})
		}
	case "stalker":
		var set xtreamSettings
		json.Unmarshal(sub.Settings, &set)
		if p.Server != nil {
			set.Server = strings.TrimSpace(*p.Server)
		}
		if p.MAC != nil && *p.MAC != "" {
			mac, err := stalker.NormalizeMAC(*p.MAC)
			if err != nil {
				return invalid("%v", err)
			}
			sec.MAC = mac
		}
		if p.Server != nil {
			if err := noCredentialsIn(set.Server); err != nil {
				return err
			}
		}
		if _, err := stalker.New(set.Server, sec.MAC, nil); err != nil {
			return invalid("%v", err)
		}
		settings, _ = json.Marshal(set)
		s.stalkerTokens.Delete(id)
	}
	rawSecret, _ := json.Marshal(sec)
	secret = s.box.Seal(rawSecret, subOwner(id))
	return s.st.UpdateSubscription(ctx, id, p.Name, p.VPNID, settings, secret)
}

// UpdateVPN renames a connection and/or replaces its configuration; a
// running tunnel is closed so the new configuration is used next time.
func (s *Service) UpdateVPN(ctx context.Context, id int64, name, conf *string) error {
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || utf8.RuneCountInString(n) > 80 {
			return invalid("nome della VPN obbligatorio (max 80 caratteri)")
		}
		name = &n
	}
	var sealed []byte
	if conf != nil && strings.TrimSpace(*conf) != "" {
		if v, err := s.st.VPN(ctx, id); err != nil {
			return err
		} else if v.Kind == "system" {
			return invalid("una VPN esterna non ha una configurazione da caricare")
		}
		if _, err := vpn.ParseConfig(strings.NewReader(*conf)); err != nil {
			return invalid("configurazione WireGuard non valida: %v", err)
		}
		sealed = s.box.Seal([]byte(*conf), vpnOwner(id))
	}
	if err := s.st.UpdateVPN(ctx, id, name, sealed); err != nil {
		return err
	}
	if sealed != nil {
		s.closeTunnel(id) // the watchdog reconnects with the new configuration
	}
	return nil
}

// SubscriptionView adds catalog counts.
type SubscriptionView struct {
	store.Subscription
	Counts  map[string]int `json:"counts"`
	Syncing bool           `json:"syncing"`
}

func (s *Service) Subscriptions(ctx context.Context) ([]SubscriptionView, error) {
	subs, err := s.st.Subscriptions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SubscriptionView, 0, len(subs))
	for _, sub := range subs {
		counts, err := s.st.Counts(ctx, sub.ID)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		syncing := s.syncing[sub.ID]
		s.mu.Unlock()
		sub.Settings = withoutCredentials(sub.Settings)
		sub.SyncError = TranslateText(LangOf(ctx), sub.SyncError) // saved in Italian
		out = append(out, SubscriptionView{Subscription: sub, Counts: counts, Syncing: syncing})
	}
	return out, nil
}

func (s *Service) DeleteSubscription(ctx context.Context, id int64) error {
	if err := s.st.DeleteSubscription(ctx, id); err != nil {
		return err
	}
	// Variants of removed items may have been primary for other
	// subscriptions' groups.
	_, err := s.ApplySelection(ctx)
	return err
}

// ProviderError is a failure of the provider or of its VPN (refused
// credentials, an answer that cannot be read, a host that does not
// answer): not a fault of VodMax (HTTP 502, not 500).
type ProviderError struct{ Err error }

func (e *ProviderError) Error() string { return e.Err.Error() }
func (e *ProviderError) Unwrap() error { return e.Err }

// ErrBusy means a synchronisation of the same subscription is running.
var ErrBusy = errors.New("sincronizzazione già in corso")

// SyncResult reports a completed synchronisation.
type SyncResult struct {
	store.ImportResult
	Skipped    int           `json:"skipped"`
	Selection  catalog.Stats `json:"selection"`
	Guide      *GuideResult  `json:"guide,omitempty"`
	GuideError string        `json:"guide_error,omitempty"`
	Duration   time.Duration `json:"duration_ms"`
}

// Sync reads the full catalog of a subscription through its VPN and
// replaces the stored one. allowDrop accepts a catalog that shrank by more
// than half.
func (s *Service) Sync(ctx context.Context, id int64, allowDrop bool) (SyncResult, error) {
	s.mu.Lock()
	if s.syncing[id] {
		s.mu.Unlock()
		return SyncResult{}, ErrBusy
	}
	s.syncing[id] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.syncing, id)
		s.mu.Unlock()
	}()

	started := time.Now()
	if sub, err := s.st.Subscription(ctx, id); err == nil && sub.Kind == "local" {
		res, err := s.syncLocal(ctx, sub, allowDrop)
		if err != nil {
			s.st.SetSyncResult(context.Background(), id, "error", err.Error(), nil)
			return SyncResult{}, err
		}
		if err := s.st.SetSyncResult(ctx, id, "ok", "", nil); err != nil {
			return SyncResult{}, err
		}
		if _, err := s.ApplySelection(ctx); err != nil {
			return SyncResult{}, err
		}
		if res.Selection, err = s.SubscriptionStats(ctx, id, nil); err != nil {
			return SyncResult{}, err
		}
		res.Duration = time.Since(started) / time.Millisecond
		return res, nil
	}
	snap, err := s.fetch(ctx, id)
	if err != nil {
		var account json.RawMessage
		if snap != nil {
			account = snap.Account
		}
		s.st.SetSyncResult(context.Background(), id, "error", err.Error(), account)
		var bad ErrInvalid
		if errors.As(err, &bad) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return SyncResult{}, err
		}
		return SyncResult{}, &ProviderError{Err: err}
	}
	res, err := s.st.ReplaceCatalog(ctx, id, snap.Kinds, snap.Categories, snap.Items, allowDrop)
	if err != nil {
		s.st.SetSyncResult(context.Background(), id, "error", err.Error(), snap.Account)
		return SyncResult{}, err
	}
	// Hidden titles and watch progress from a restored backup.
	if err := s.st.ApplyPendingRestore(ctx); err != nil {
		return SyncResult{}, err
	}
	if err := s.st.SetSyncResult(ctx, id, "ok", "", snap.Account); err != nil {
		return SyncResult{}, err
	}
	if _, err := s.ApplySelection(ctx); err != nil {
		return SyncResult{}, err
	}
	stats, err := s.SubscriptionStats(ctx, id, nil)
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{ImportResult: res, Skipped: snap.Skipped, Selection: stats}
	// The guide is refreshed with the catalog; a guide failure does not
	// invalidate the catalog.
	if sub, err := s.st.Subscription(ctx, id); err == nil && sub.Kind == "xtream" {
		g, err := s.SyncGuide(ctx, id)
		s.guideDone(ctx, id, err)
		if err != nil {
			result.GuideError = Localize(LangOf(ctx), err)
		} else {
			result.Guide = &g
			s.syncExternalGuides(ctx)
		}
	}
	result.Duration = time.Since(started) / time.Millisecond
	return result, nil
}

func (s *Service) fetch(ctx context.Context, id int64) (*catalog.Snapshot, error) {
	sub, err := s.st.Subscription(ctx, id)
	if err != nil {
		return nil, err
	}
	raw, err := s.box.Open(sub.Secret, subOwner(id))
	if err != nil {
		return nil, err
	}
	var sec subscriptionSecret
	if err := json.Unmarshal(raw, &sec); err != nil {
		return nil, err
	}
	tun, err := s.tunnel(ctx, sub.VPNID)
	if err != nil {
		return nil, fmt.Errorf("VPN: %w", err)
	}
	httpClient := provider.NewHTTPClient(tun, 10*time.Minute)

	switch sub.Kind {
	case "xtream":
		var set xtreamSettings
		if err := json.Unmarshal(sub.Settings, &set); err != nil {
			return nil, err
		}
		client, err := xtream.New(set.Server, set.Username, sec.Password, httpClient)
		if err != nil {
			return nil, err
		}
		return catalog.FromXtream(ctx, client)
	case "m3u":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, sec.URL, nil)
		if err != nil {
			return nil, provider.Redact(err)
		}
		req.Header.Set("User-Agent", "VodMax")
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("playlist: %w", provider.Redact(err))
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("playlist: risposta HTTP %d", resp.StatusCode)
		}
		p, err := m3u.Parse(io.LimitReader(resp.Body, 1<<30), m3u.DefaultLimits)
		if err != nil {
			return nil, err
		}
		if len(p.Entries) == 0 {
			return nil, errors.New("la playlist non contiene canali né titoli")
		}
		return catalog.FromM3U(p, s.box.Seal, id), nil
	case "stalker":
		client, err := s.stalkerClient(ctx, sub, sec, httpClient)
		if err != nil {
			return nil, err
		}
		snap, err := catalog.FromStalker(ctx, client, s.box.Seal, id)
		s.stalkerTokens.Store(id, client.Token())
		return snap, err
	}
	return nil, fmt.Errorf("tipo di abbonamento sconosciuto %q", sub.Kind)
}

// MyList is the viewer's list of films and series.
func (s *Service) MyList(ctx context.Context) ([]store.CatalogItem, error) {
	v := viewerOf(ctx)
	list, err := s.st.MyList(ctx, v.ProfileID, v.Kids)
	s.catalogImages(ctx, list)
	return list, err
}

// InMyList tells whether a film or series is in the viewer's list.
func (s *Service) InMyList(ctx context.Context, itemID int64) (bool, error) {
	return s.st.InMyList(ctx, viewerOf(ctx).ProfileID, itemID)
}

// SetMyList adds a film or series to the viewer's list, or removes it.
func (s *Service) SetMyList(ctx context.Context, itemID int64, on bool) error {
	it, err := s.st.PlaybackItem(ctx, itemID)
	if err != nil {
		return err
	}
	if it.Kind != "movie" && it.Kind != "series" {
		return invalid("solo film e serie vanno nella lista")
	}
	if err := s.allowItem(ctx, itemID); err != nil {
		return err
	}
	return s.st.SetFavorite(ctx, viewerOf(ctx).ProfileID, itemID, on)
}

// Catalog and Categories pass through to the store.
func (s *Service) Catalog(ctx context.Context, q store.Query) (store.Page, error) {
	if viewerOf(ctx).Kids {
		q.NoAdult, q.All = true, false
	}
	page, err := s.st.Catalog(ctx, q)
	s.catalogImages(ctx, page.Items)
	return page, err
}

// catalogImages replaces provider image URLs with signed local ones.
// A film or series without an image of its own can still have the TMDB
// poster, when a key is configured.
func (s *Service) catalogImages(ctx context.Context, items []store.CatalogItem) {
	tmdb := false
	for _, it := range items {
		if it.Image == "" && it.TMDBID != "" {
			tmdb = s.tmdbClient(ctx) != nil
			break
		}
	}
	for i := range items {
		if items[i].Image != "" || (tmdb && items[i].TMDBID != "" && items[i].Kind != "live") {
			items[i].Logo = s.LogoURL(items[i].ID)
		}
		items[i].Image = ""
	}
}

func (s *Service) Categories(ctx context.Context, kind string, subID int64) ([]store.Category, error) {
	return s.st.Categories(ctx, kind, subID)
}

// BrowseCategories lists the categories with titles the viewer can see.
func (s *Service) BrowseCategories(ctx context.Context, kind string) ([]store.BrowseCategory, error) {
	if kind != "movie" && kind != "series" && kind != "live" {
		return nil, invalid("tipo non valido")
	}
	return s.st.BrowseCategories(ctx, kind, viewerOf(ctx).Kids)
}

// noCredentialsIn refuses a server address with a user name or password in
// it: they have their own sealed fields, while the address is shown.
func noCredentialsIn(server string) error {
	if u, err := url.Parse(strings.TrimSpace(server)); err == nil && u.User != nil {
		return invalid("l'indirizzo del server non deve contenere nome utente e password: usa i campi appositi")
	}
	return nil
}

// withoutCredentials hides a user name and password that an address saved
// by an earlier version may carry.
func withoutCredentials(settings json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(settings, &m) != nil {
		return settings
	}
	server, _ := m["server"].(string)
	u, err := url.Parse(server)
	if server == "" || err != nil || u.User == nil {
		return settings
	}
	u.User = nil
	m["server"] = u.String()
	out, err := json.Marshal(m)
	if err != nil {
		return settings
	}
	return out
}
