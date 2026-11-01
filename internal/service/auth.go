package service

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silirim/vodmax/internal/store"
)

// Devices authenticate with their own token (obtained by pairing); the
// person watching is a profile, chosen on the device and proved by a
// profile token signed for that device. The admin token file remains as
// the master key of the server (device 0).

var (
	ErrUnauthorized    = errors.New("dispositivo non abbinato o token non valido")
	ErrProfileRequired = errors.New("scegli chi sta guardando")
	ErrForbidden       = errors.New("operazione riservata a un profilo amministratore")
	ErrPIN             = errors.New("PIN errato")
	ErrPINLocked       = errors.New("troppi PIN errati: riprova tra qualche minuto")
	ErrPairing         = errors.New("codice di abbinamento sconosciuto o scaduto")
	ErrAdminPIN        = errors.New("un profilo amministratore deve avere un PIN quando ci sono altri profili")
	ErrCurrentPIN      = errors.New("il PIN attuale non è corretto")
)

const (
	MasterDevice     = 0
	pinIterations    = 100_000
	pinMaxFails      = 5
	pinLockFor       = 5 * time.Minute
	pairingTTL       = 10 * time.Minute
	pairingCollect   = 30 * time.Minute
	pairingMax       = 50
	pairingPerClient = 5
	pairingAlphabet  = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I
)

var pinFormat = regexp.MustCompile(`^[0-9]{4,8}$`)

// Identity is an authenticated request.
type Identity struct {
	DeviceID int64
	Device   string
	Lock     *int64
	Profile  *store.Profile // nil until a profile is chosen
	// NeedsPIN: an admin profile without PIN while other profiles exist.
	// Anyone could choose it, so it gets no admin rights until its PIN is
	// set (the master token keeps them).
	NeedsPIN bool
}

func (i Identity) Admin() bool {
	return i.DeviceID == MasterDevice || (i.Profile != nil && i.Profile.Kind == "admin" && !i.NeedsPIN)
}

func (i Identity) Viewer() Viewer {
	if i.Profile == nil {
		return Viewer{ProfileID: DefaultProfile}
	}
	return Viewer{ProfileID: i.Profile.ID, Kids: i.Profile.Kind == "kids"}
}

type authState struct {
	mu      sync.Mutex
	pinFail map[string]*pinFails // device:profile
	pairing map[string]*pairing  // by poll secret
}

type pinFails struct {
	n     int
	until time.Time
}

type pairing struct {
	code    string
	client  string
	expires time.Time
	token   string // set when approved
	device  int64
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Authenticate resolves a bearer token and an optional profile token.
func (s *Service) Authenticate(ctx context.Context, token, profileToken, master string) (Identity, error) {
	var id Identity
	switch {
	case token == "":
		return id, ErrUnauthorized
	case subtle.ConstantTimeCompare([]byte(token), []byte(master)) == 1:
		id.DeviceID, id.Device = MasterDevice, "Token di amministrazione"
	default:
		d, err := s.st.DeviceByToken(ctx, hashToken(token))
		if errors.Is(err, store.ErrNotFound) {
			return id, ErrUnauthorized
		}
		if err != nil {
			return id, err
		}
		s.st.TouchDevice(context.WithoutCancel(ctx), d.ID, time.Now())
		id.DeviceID, id.Device, id.Lock = d.ID, d.Name, d.ProfileLock
	}
	p, err := s.resolveProfile(ctx, id, profileToken)
	if err != nil {
		return id, err
	}
	id.Profile = p
	if p != nil && p.Kind == "admin" && !p.HasPIN && id.DeviceID != MasterDevice {
		if list, err := s.st.Profiles(ctx); err == nil && len(list) > 1 {
			id.NeedsPIN = true
		}
	}
	return id, nil
}

// resolveProfile: the signed profile token if valid; otherwise, when the
// choice is obvious (a single possible profile without PIN), that one.
func (s *Service) resolveProfile(ctx context.Context, id Identity, profileToken string) (*store.Profile, error) {
	if profileToken != "" {
		pid, sig, ok := strings.Cut(profileToken, ".")
		n, err := strconv.ParseInt(pid, 10, 64)
		if ok && err == nil {
			p, err := s.st.Profile(ctx, n)
			if err == nil && (id.Lock == nil || *id.Lock == p.ID) &&
				hmac.Equal([]byte(sig), []byte(s.profileSignature(id.DeviceID, p))) {
				return &p, nil
			}
		}
	}
	candidates, err := s.selectable(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 1 && !candidates[0].HasPIN {
		return &candidates[0], nil
	}
	if id.DeviceID == MasterDevice {
		for _, p := range candidates {
			if p.Kind == "admin" {
				return &p, nil
			}
		}
	}
	return nil, nil
}

func (s *Service) selectable(ctx context.Context, id Identity) ([]store.Profile, error) {
	list, err := s.st.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	if id.Lock != nil {
		kept := list[:0]
		for _, p := range list {
			if p.ID == *id.Lock {
				kept = append(kept, p)
			}
		}
		list = kept
	}
	return list, nil
}

func (s *Service) profileSignature(deviceID int64, p store.Profile) string {
	m := hmac.New(sha256.New, s.box.Derive("profile-token"))
	fmt.Fprintf(m, "%d|%d|%d", deviceID, p.ID, p.PINEpoch)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// SelectableProfiles lists the profiles a device may choose.
func (s *Service) SelectableProfiles(ctx context.Context, id Identity) ([]store.Profile, error) {
	list, err := s.selectable(ctx, id)
	if list == nil {
		list = []store.Profile{}
	}
	return list, err
}

// SelectProfile checks the PIN and returns the profile token for the device.
func (s *Service) SelectProfile(ctx context.Context, id Identity, profileID int64, pin string) (string, error) {
	p, err := s.st.Profile(ctx, profileID)
	if err != nil {
		return "", err
	}
	if id.Lock != nil && *id.Lock != p.ID {
		return "", store.ErrNotFound
	}
	if p.HasPIN {
		key := fmt.Sprintf("%d:%d", id.DeviceID, p.ID)
		s.auth.mu.Lock()
		if s.auth.pinFail == nil {
			s.auth.pinFail = map[string]*pinFails{}
		}
		f := s.auth.pinFail[key]
		if f != nil && time.Now().Before(f.until) {
			s.auth.mu.Unlock()
			return "", ErrPINLocked
		}
		s.auth.mu.Unlock()
		if !checkPIN(p.PINHash, pin) {
			s.auth.mu.Lock()
			if f == nil {
				f = &pinFails{}
				s.auth.pinFail[key] = f
			}
			f.n++
			if f.n >= pinMaxFails {
				f.n, f.until = 0, time.Now().Add(pinLockFor)
			}
			s.auth.mu.Unlock()
			return "", ErrPIN
		}
		s.auth.mu.Lock()
		delete(s.auth.pinFail, key)
		s.auth.mu.Unlock()
	}
	return strconv.FormatInt(p.ID, 10) + "." + s.profileSignature(id.DeviceID, p), nil
}

func hashPIN(pin string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	k, err := pbkdf2.Key(sha256.New, pin, salt, pinIterations, 32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(salt) + "$" + hex.EncodeToString(k), nil
}

func checkPIN(stored, pin string) bool {
	saltHex, hashHex, ok := strings.Cut(stored, "$")
	salt, err1 := hex.DecodeString(saltHex)
	want, err2 := hex.DecodeString(hashHex)
	if !ok || err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pin, salt, pinIterations, 32)
	return err == nil && hmac.Equal(got, want)
}

// ProfileInput creates or changes a profile. PIN: nil keeps it, "" removes it.
type ProfileInput struct {
	Name   *string `json:"name"`
	Kind   *string `json:"kind"`
	PIN    *string `json:"pin"`
	Color  *string `json:"color"`
	Avatar *string `json:"avatar"`
}

// Avatars and colours a profile can choose (icons of the web interface).
var (
	Avatars = []string{"cat", "dog", "rabbit", "panda", "bird", "fish", "turtle", "squirrel", "snail", "ghost",
		"rocket", "crown", "gamepad-2", "music", "palette", "trophy", "bike", "car", "plane", "popcorn",
		"pizza", "ice-cream-cone", "flower-2", "leaf", "sun", "moon", "snowflake", "zap", "heart", "sparkles"}
	AvatarColors = []string{"#00d6ca", "#3d8bff", "#8b5cf6", "#ec4899", "#ff4d4f", "#ff8a1f", "#f5c518", "#22c55e"}
)

func (s *Service) applyProfile(p *store.Profile, in ProfileInput) (pinChanged bool, err error) {
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if p.Name == "" || len(p.Name) > 40 {
		return false, invalid("nome del profilo obbligatorio (max 40 caratteri)")
	}
	if in.Kind != nil {
		p.Kind = *in.Kind
	}
	if p.Kind != "admin" && p.Kind != "standard" && p.Kind != "kids" {
		return false, invalid("tipo di profilo non valido: admin, standard o kids")
	}
	if in.Color != nil {
		if *in.Color != "" && !slices.Contains(AvatarColors, *in.Color) {
			return false, invalid("colore non disponibile")
		}
		p.Color = *in.Color
	}
	if in.Avatar != nil {
		if *in.Avatar != "" && !slices.Contains(Avatars, *in.Avatar) {
			return false, invalid("avatar non disponibile")
		}
		p.Avatar = *in.Avatar
	}
	if in.PIN != nil {
		if *in.PIN == "" {
			p.PINHash = ""
		} else {
			if !pinFormat.MatchString(*in.PIN) {
				return false, invalid("il PIN deve avere da 4 a 8 cifre")
			}
			if p.PINHash, err = hashPIN(*in.PIN); err != nil {
				return false, err
			}
		}
		pinChanged = true
	}
	return pinChanged, nil
}

func (s *Service) AddProfile(ctx context.Context, in ProfileInput) (int64, error) {
	p := store.Profile{Kind: "standard"}
	if _, err := s.applyProfile(&p, in); err != nil {
		return 0, err
	}
	// With a second profile every admin needs a PIN.
	list, err := s.st.Profiles(ctx)
	if err != nil {
		return 0, err
	}
	for _, x := range append(list, p) {
		if x.Kind == "admin" && x.PINHash == "" && len(list) > 0 {
			return 0, ErrAdminPIN
		}
	}
	id, err := s.st.AddProfile(ctx, p)
	if errors.Is(err, store.ErrNameTaken) {
		return 0, invalid("%v", err)
	}
	return id, err
}

func (s *Service) UpdateProfile(ctx context.Context, id int64, in ProfileInput) error {
	p, err := s.st.Profile(ctx, id)
	if err != nil {
		return err
	}
	wasAdmin := p.Kind == "admin"
	changed, err := s.applyProfile(&p, in)
	if err != nil {
		return err
	}
	if wasAdmin && p.Kind != "admin" {
		if err := s.keepAnAdmin(ctx, id); err != nil {
			return err
		}
	}
	if p.Kind == "admin" && p.PINHash == "" {
		if list, err := s.st.Profiles(ctx); err != nil {
			return err
		} else if len(list) > 1 {
			return ErrAdminPIN
		}
	}
	err = s.st.UpdateProfile(ctx, p, changed)
	if errors.Is(err, store.ErrNameTaken) {
		return invalid("%v", err)
	}
	return err
}

// OwnProfileInput is what a person changes on their own profile: avatar,
// colour and (not on a kids profile) the PIN, with the current one.
type OwnProfileInput struct {
	Avatar     *string `json:"avatar"`
	Color      *string `json:"color"`
	PIN        *string `json:"pin"`
	CurrentPIN string  `json:"current_pin"`
}

func (s *Service) UpdateOwnProfile(ctx context.Context, id Identity, in OwnProfileInput) error {
	if id.Profile == nil {
		return ErrProfileRequired
	}
	p, err := s.st.Profile(ctx, id.Profile.ID)
	if err != nil {
		return err
	}
	change := ProfileInput{Avatar: in.Avatar, Color: in.Color}
	if in.PIN != nil {
		if p.Kind == "kids" {
			return ErrForbidden
		}
		if p.HasPIN && !checkPIN(p.PINHash, in.CurrentPIN) {
			return ErrCurrentPIN
		}
		change.PIN = in.PIN
	}
	changed, err := s.applyProfile(&p, change)
	if err != nil {
		return err
	}
	if p.Kind == "admin" && p.PINHash == "" {
		if list, err := s.st.Profiles(ctx); err != nil {
			return err
		} else if len(list) > 1 {
			return ErrAdminPIN
		}
	}
	return s.st.UpdateProfile(ctx, p, changed)
}

func (s *Service) DeleteProfile(ctx context.Context, id int64) error {
	p, err := s.st.Profile(ctx, id)
	if err != nil {
		return err
	}
	if p.Kind == "admin" {
		if err := s.keepAnAdmin(ctx, id); err != nil {
			return err
		}
	}
	// A device locked to this profile would be left unlocked (it would see
	// every profile), and an account of the home login would move to the
	// next profile that gets the same id: both are fixed by the admin first.
	devices, err := s.st.Devices(ctx)
	if err != nil {
		return err
	}
	for _, d := range devices {
		if d.ProfileLock != nil && *d.ProfileLock == id {
			return invalid("il dispositivo «%s» è bloccato su questo profilo: sbloccalo o rimuovilo prima", d.Name)
		}
	}
	sso, _, err := s.SSOSettings(ctx)
	if err != nil {
		return err
	}
	for _, a := range sso.Accounts {
		if a.ProfileID == id {
			return invalid("l'account «%s» dell'accesso di casa è collegato a questo profilo: collegalo a un altro profilo prima", a.Account)
		}
	}
	return s.st.DeleteProfile(ctx, id)
}

func (s *Service) keepAnAdmin(ctx context.Context, except int64) error {
	list, err := s.st.Profiles(ctx)
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.Kind == "admin" && p.ID != except {
			return nil
		}
	}
	return invalid("deve restare almeno un profilo amministratore")
}

// ---- Devices and pairing ----

func (s *Service) Devices(ctx context.Context) ([]store.Device, error) {
	list, err := s.st.Devices(ctx)
	if list == nil {
		list = []store.Device{}
	}
	return list, err
}

func (s *Service) UpdateDevice(ctx context.Context, id int64, name string, lock *int64) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return invalid("nome del dispositivo obbligatorio (max 60 caratteri)")
	}
	if lock != nil {
		if _, err := s.st.Profile(ctx, *lock); err != nil {
			return invalid("profilo inesistente")
		}
	}
	return s.st.UpdateDevice(ctx, id, name, lock)
}

func (s *Service) DeleteDevice(ctx context.Context, id int64) error {
	return s.st.DeleteDevice(ctx, id)
}

// PairingRequest is what a new device shows and polls.
type PairingRequest struct {
	Code      string `json:"code"`
	Poll      string `json:"poll"`
	ExpiresIn int    `json:"expires_in"`
}

// StartPairing creates a code for a device that is not paired yet.
func (s *Service) StartPairing(client string) (PairingRequest, error) {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	if s.auth.pairing == nil {
		s.auth.pairing = map[string]*pairing{}
	}
	now := time.Now()
	perClient := 0
	for k, p := range s.auth.pairing {
		if now.After(p.expires) {
			delete(s.auth.pairing, k)
		} else if p.client == client {
			perClient++
		}
	}
	if len(s.auth.pairing) >= pairingMax || perClient >= pairingPerClient {
		return PairingRequest{}, ErrBusy
	}
	code := make([]byte, 6)
	for i := range code {
		code[i] = pairingAlphabet[randIntn(len(pairingAlphabet))]
	}
	c := string(code[:3]) + "-" + string(code[3:])
	poll := randomToken()
	s.auth.pairing[poll] = &pairing{code: c, client: client, expires: now.Add(pairingTTL)}
	return PairingRequest{Code: c, Poll: poll, ExpiresIn: int(pairingTTL.Seconds())}, nil
}

// PairingResult returns the device token once the code is approved
// (empty while waiting); the token is handed out a single time.
func (s *Service) PairingResult(poll string) (token string, device int64, err error) {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	p := s.auth.pairing[poll]
	if p == nil || time.Now().After(p.expires) {
		delete(s.auth.pairing, poll)
		return "", 0, ErrPairing
	}
	if p.token == "" {
		return "", 0, nil
	}
	delete(s.auth.pairing, poll)
	return p.token, p.device, nil
}

// ApprovePairing pairs the device showing code.
func (s *Service) ApprovePairing(ctx context.Context, code, name string, lock *int64) (store.Device, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) == 6 {
		code = code[:3] + "-" + code[3:]
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return store.Device{}, invalid("nome del dispositivo obbligatorio (max 60 caratteri)")
	}
	if lock != nil {
		if _, err := s.st.Profile(ctx, *lock); err != nil {
			return store.Device{}, invalid("profilo inesistente")
		}
	}
	s.auth.mu.Lock()
	var found *pairing
	for _, p := range s.auth.pairing {
		if p.token == "" && time.Now().Before(p.expires) && subtle.ConstantTimeCompare([]byte(p.code), []byte(code)) == 1 {
			found = p
		}
	}
	s.auth.mu.Unlock()
	if found == nil {
		return store.Device{}, ErrPairing
	}
	token := randomToken()
	id, err := s.st.AddDevice(ctx, name, hashToken(token))
	if err != nil {
		return store.Device{}, err
	}
	if lock != nil {
		if err := s.st.UpdateDevice(ctx, id, name, lock); err != nil {
			return store.Device{}, err
		}
	}
	s.auth.mu.Lock()
	found.token, found.device = token, id
	// Time to collect it: the app may be in the background while the
	// user sends the code from another app.
	found.expires = time.Now().Add(pairingCollect)
	s.auth.mu.Unlock()
	return store.Device{ID: id, Name: name, ProfileLock: lock, CreatedAt: time.Now().Unix()}, nil
}

// randIntn returns a uniform random number in [0, n) from crypto/rand.
func randIntn(n int) int {
	limit := 256 - 256%n
	var b [1]byte
	for {
		rand.Read(b[:])
		if int(b[0]) < limit {
			return int(b[0]) % n
		}
	}
}
