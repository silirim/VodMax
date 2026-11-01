package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/silirim/vodmax/internal/store"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// call makes a request as a paired device, optionally with a profile.
func (e *env) call(t *testing.T, method, path string, body any, device, profile string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	if device != "" {
		req.Header.Set("Authorization", "Bearer "+device)
	}
	if profile != "" {
		req.Header.Set("X-Profile", profile)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func pair(t *testing.T, e *env, name string, lock *int64) string {
	t.Helper()
	code, body := e.call(t, "POST", "/api/v1/pair", nil, "", "")
	var req struct{ Code, Poll string }
	if code != 201 || json.Unmarshal(body, &req) != nil || len(req.Code) != 7 {
		t.Fatalf("pair start: %d %s", code, body)
	}
	if code, _ := e.call(t, "GET", "/api/v1/pair/"+req.Poll, nil, "", ""); code != 202 {
		t.Fatalf("pending: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/v1/pair/approve", map[string]any{"code": "ZZZ-ZZZ", "name": name}, true); code != 404 {
		t.Fatalf("unknown code: %d", code)
	}
	// Typed without the dash and in lower case.
	typed := strings.ToLower(strings.ReplaceAll(req.Code, "-", ""))
	if code, body := e.do(t, "POST", "/api/v1/pair/approve", map[string]any{"code": typed, "name": name, "profile_lock": lock}, true); code != 201 {
		t.Fatalf("approve: %d %s", code, body)
	}
	code, body = e.call(t, "GET", "/api/v1/pair/"+req.Poll, nil, "", "")
	var res struct{ Token string }
	if code != 200 || json.Unmarshal(body, &res) != nil || res.Token == "" {
		t.Fatalf("result: %d %s", code, body)
	}
	if code, _ := e.call(t, "GET", "/api/v1/pair/"+req.Poll, nil, "", ""); code != 404 {
		t.Fatal("token handed out twice")
	}
	return res.Token
}

func selectProfile(t *testing.T, e *env, device string, id int64, pin string) (int, string) {
	t.Helper()
	code, body := e.call(t, "POST", "/api/v1/profiles/"+strconv.FormatInt(id, 10)+"/select", map[string]string{"pin": pin}, device, "")
	var out struct {
		ProfileToken string `json:"profile_token"`
	}
	json.Unmarshal(body, &out)
	return code, out.ProfileToken
}

func TestProfilesDevicesAndKids(t *testing.T) {
	e, _ := tvSetup(t)
	// Horror is an adult category, shown to adults by the rules.
	e.do(t, "PUT", "/api/v1/subscriptions/1/categories/movie/10", map[string]any{"adult": true}, true)
	e.do(t, "PUT", "/api/v1/subscriptions/1/categories/movie/11", map[string]any{"adult": true}, true)
	e.do(t, "PUT", "/api/v1/subscriptions/1/rules", map[string]any{"adult": true}, true)
	film := strconv.FormatInt(findItem(t, e, "movie", "1408"), 10)

	addProfile := func(body map[string]any) int64 {
		code, out := e.do(t, "POST", "/api/v1/profiles", body, true)
		var r struct{ ID int64 }
		if code != 201 || json.Unmarshal(out, &r) != nil {
			t.Fatalf("add profile: %d %s", code, out)
		}
		return r.ID
	}
	// A second profile needs the admin's PIN first.
	if code, _ := e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "Bimbi", "kind": "kids"}, true); code != 400 {
		t.Fatalf("profile added while the admin has no PIN: %d", code)
	}
	e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": "9753"}, true)
	kids := addProfile(map[string]any{"name": "Bimbi", "kind": "kids"})
	marco := addProfile(map[string]any{"name": "Marco", "kind": "standard", "pin": "2468"})
	if code, _ := e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "Marco"}, true); code != 400 {
		t.Fatal("duplicate name accepted")
	}
	if code, _ := e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "X", "pin": "12"}, true); code != 400 {
		t.Fatal("short PIN accepted")
	}

	// Unpaired: nothing.
	if code, _ := e.call(t, "GET", "/api/v1/catalog/movie", nil, "nonvalido", ""); code != 401 {
		t.Fatal("unknown device accepted")
	}
	salotto := pair(t, e, "TV salotto", nil)
	// Three profiles: the device must choose.
	code, body := e.call(t, "GET", "/api/v1/catalog/movie", nil, salotto, "")
	if code != 403 || !strings.Contains(string(body), "profile_required") {
		t.Fatalf("no profile: %d %s", code, body)
	}
	if _, body = e.call(t, "GET", "/api/v1/profiles", nil, salotto, ""); strings.Count(string(body), `"id"`) != 3 || strings.Contains(string(body), "pin_hash") {
		t.Fatalf("profiles: %s", body)
	}

	// PIN: wrong, locked after five errors, right one refused while locked.
	if code, _ := selectProfile(t, e, salotto, marco, "1111"); code != 403 {
		t.Fatalf("wrong PIN: %d", code)
	}
	for range 4 {
		selectProfile(t, e, salotto, marco, "1111")
	}
	if code, _ := selectProfile(t, e, salotto, marco, "2468"); code != 429 {
		t.Fatalf("not locked: %d", code)
	}
	// Another device is not affected.
	studio := pair(t, e, "PC studio", nil)
	code, marcoTok := selectProfile(t, e, studio, marco, "2468")
	if code != 200 || marcoTok == "" {
		t.Fatalf("right PIN: %d", code)
	}
	// A profile token only works on the device it was issued to.
	if code, _ := e.call(t, "GET", "/api/v1/continue", nil, salotto, marcoTok); code != 403 {
		t.Fatalf("profile token reused on another device: %d", code)
	}
	// Separate progress per profile.
	e.call(t, "PUT", "/api/v1/progress/movie/"+film, map[string]int{"position": 700, "duration": 6000}, studio, marcoTok)
	if _, body = e.do(t, "GET", "/api/v1/continue", nil, true); strings.Contains(string(body), "1408") {
		t.Fatalf("Marco's progress shown to the main profile: %s", body)
	}
	if _, body = e.call(t, "GET", "/api/v1/continue", nil, studio, marcoTok); !strings.Contains(string(body), `"position":700`) {
		t.Fatalf("Marco's continue: %s", body)
	}
	// Standard profiles do not administer.
	if code, _ := e.call(t, "GET", "/api/v1/subscriptions", nil, studio, marcoTok); code != 403 {
		t.Fatalf("standard profile reached settings: %d", code)
	}

	// Kids: adult films neither listed nor playable, no recording.
	code, kidsTok := selectProfile(t, e, salotto, kids, "")
	if code != 200 {
		t.Fatalf("kids select: %d", code)
	}
	if _, body = e.call(t, "GET", "/api/v1/catalog/movie", nil, salotto, kidsTok); strings.Contains(string(body), "1408") {
		t.Fatalf("adult film listed to kids: %s", body)
	}
	if _, body = e.call(t, "GET", "/api/v1/catalog/movie?all=true", nil, salotto, kidsTok); strings.Contains(string(body), "1408") {
		t.Fatal("adult film listed to kids with all=true")
	}
	if code, _ := e.call(t, "POST", "/api/v1/vod/movie/"+film+"/session", map[string]string{"viewer": "salotto-tv-01"}, salotto, kidsTok); code != 404 {
		t.Fatalf("adult film opened by kids: %d", code)
	}
	if _, body = e.call(t, "GET", "/api/v1/catalog/series", nil, salotto, kidsTok); !strings.Contains(string(body), "Twin Peaks") {
		t.Fatalf("kids see nothing: %s", body)
	}
	tv := channels(t, e, "?section=tv")
	if code, _ := e.call(t, "POST", "/api/v1/recordings", map[string]any{"channel_id": tv[0].ID}, salotto, kidsTok); code != 403 {
		t.Fatalf("kids scheduled a recording: %d", code)
	}
	// Adults still see the film.
	if _, body = e.call(t, "GET", "/api/v1/catalog/movie", nil, studio, marcoTok); !strings.Contains(string(body), "1408") {
		t.Fatalf("adult profile lost the film: %s", body)
	}

	// A device locked on the kids profile: no choice, no other profile.
	var lock = kids
	tablet := pair(t, e, "Tablet bimbi", &lock)
	if code, body := e.call(t, "GET", "/api/v1/catalog/series", nil, tablet, ""); code != 200 {
		t.Fatalf("locked device without choosing: %d %s", code, body)
	}
	if code, _ := selectProfile(t, e, tablet, 1, ""); code != 404 {
		t.Fatalf("locked device chose another profile: %d", code)
	}
	if _, body = e.call(t, "GET", "/api/v1/me", nil, tablet, ""); !strings.Contains(string(body), `"kind":"kids"`) || !strings.Contains(string(body), `"admin":false`) {
		t.Fatalf("me: %s", body)
	}

	// Changing the PIN logs the profile out everywhere.
	e.do(t, "PATCH", "/api/v1/profiles/"+strconv.FormatInt(marco, 10), map[string]any{"pin": "1357"}, true)
	if code, _ := e.call(t, "GET", "/api/v1/continue", nil, studio, marcoTok); code != 403 {
		t.Fatalf("old profile token still valid after PIN change: %d", code)
	}

	// Revoking a device.
	_, body = e.do(t, "GET", "/api/v1/devices", nil, true)
	var devs []struct {
		ID   int64
		Name string
	}
	json.Unmarshal(body, &devs)
	if len(devs) != 3 {
		t.Fatalf("devices: %s", body)
	}
	for _, d := range devs {
		if d.Name == "PC studio" {
			e.do(t, "DELETE", "/api/v1/devices/"+strconv.FormatInt(d.ID, 10), nil, true)
		}
	}
	if code, _ := e.call(t, "GET", "/api/v1/me", nil, studio, ""); code != 401 {
		t.Fatalf("revoked device still works: %d", code)
	}

	// The last admin profile cannot go.
	if code, _ := e.do(t, "DELETE", "/api/v1/profiles/1", nil, true); code != 400 {
		t.Fatalf("last admin deleted: %d", code)
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"kind": "kids"}, true); code != 400 {
		t.Fatalf("last admin demoted: %d", code)
	}
}

func TestWarnAdultWithoutPINWhenKidsExist(t *testing.T) {
	e := setup(t, true)
	if _, body := e.do(t, "GET", "/api/v1/status", nil, true); strings.Contains(string(body), "PIN") {
		t.Fatalf("warning with one profile: %s", body)
	}
	e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": "4321"}, true)
	e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "Marco"}, true)
	if _, body := e.do(t, "GET", "/api/v1/status", nil, true); strings.Contains(string(body), "PIN") {
		t.Fatalf("warning without kids: %s", body)
	}
	e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "Bimbi", "kind": "kids"}, true)
	if _, body := e.do(t, "GET", "/api/v1/status", nil, true); !strings.Contains(string(body), "Marco") {
		t.Fatalf("no warning: %s", body)
	}
	// The admin keeps its PIN while other profiles exist.
	if code, _ := e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": ""}, true); code != 400 {
		t.Fatalf("admin PIN removed: %d", code)
	}
}

// An installation from before the rule: an admin profile without PIN and
// other profiles. Whoever chooses it gets no admin rights until a PIN is
// set; then it asks for that PIN.
func TestAdminWithoutPINMustSetOne(t *testing.T) {
	e := setup(t, true)
	st, err := store.Open(e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProfile(context.Background(), store.Profile{Name: "Marco", Kind: "standard"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if _, body := e.do(t, "GET", "/api/v1/status", nil, true); !strings.Contains(string(body), "Principale") {
		t.Fatalf("no problem reported: %s", body)
	}
	phone := pair(t, e, "Telefono", nil)
	code, tok := selectProfile(t, e, phone, 1, "")
	if code != 200 {
		t.Fatalf("select: %d", code)
	}
	_, me := e.call(t, "GET", "/api/v1/me", nil, phone, tok)
	if !strings.Contains(string(me), `"pin_required":true`) || !strings.Contains(string(me), `"admin":false`) {
		t.Fatalf("me: %s", me)
	}
	if code, _ := e.call(t, "GET", "/api/v1/subscriptions", nil, phone, tok); code != 403 {
		t.Fatalf("settings without PIN: %d", code)
	}
	if code, _ := e.call(t, "PUT", "/api/v1/profile", map[string]string{"pin": "12"}, phone, tok); code != 400 {
		t.Fatalf("short PIN: %d", code)
	}
	if code, b := e.call(t, "PUT", "/api/v1/profile", map[string]string{"pin": "2580"}, phone, tok); code != 204 {
		t.Fatalf("set PIN: %d %s", code, b)
	}
	// The PIN change ends the earlier choice: now it takes the PIN.
	if code, _ := selectProfile(t, e, phone, 1, ""); code != 403 {
		t.Fatalf("admin without its PIN: %d", code)
	}
	_, tok = selectProfile(t, e, phone, 1, "2580")
	if code, _ := e.call(t, "GET", "/api/v1/subscriptions", nil, phone, tok); code != 200 {
		t.Fatalf("settings with PIN: %d", code)
	}
	// Changing it again needs the current one.
	if code, _ := e.call(t, "PUT", "/api/v1/profile", map[string]string{"pin": "1111", "current_pin": "0000"}, phone, tok); code != 403 {
		t.Fatalf("PIN changed without the current one: %d", code)
	}
}

func TestOwnAvatar(t *testing.T) {
	e := setup(t, true)
	e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": "4321"}, true)
	kid := addKids(t, e)
	if code, b := e.call(t, "PUT", "/api/v1/profile", map[string]string{"avatar": "rocket", "color": "#ec4899"}, kid.device, kid.profile); code != 204 {
		t.Fatalf("avatar: %d %s", code, b)
	}
	_, b := e.call(t, "GET", "/api/v1/profiles", nil, kid.device, "")
	if !strings.Contains(string(b), `"avatar":"rocket"`) || !strings.Contains(string(b), `"color":"#ec4899"`) {
		t.Fatalf("profiles: %s", b)
	}
	if code, _ := e.call(t, "PUT", "/api/v1/profile", map[string]string{"avatar": "../../etc"}, kid.device, kid.profile); code != 400 {
		t.Fatalf("unknown avatar: %d", code)
	}
	// A kids profile chooses its avatar, not its PIN.
	if code, _ := e.call(t, "PUT", "/api/v1/profile", map[string]string{"pin": "1234"}, kid.device, kid.profile); code != 403 {
		t.Fatalf("kids PIN: %d", code)
	}
	_, b = e.call(t, "GET", "/api/v1/avatars", nil, kid.device, "")
	if !strings.Contains(string(b), "rocket") {
		t.Fatalf("avatars: %s", b)
	}
}

// A deleted profile's id comes back with the next profile (SQLite reuses
// it): a token chosen for the old one must not open the new one, and a
// profile still used by a locked device or a home-login account is not
// deleted silently.
func TestDeletedProfileTokenAndLinks(t *testing.T) {
	e, _ := tvSetup(t)
	e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": "9753"}, true)
	add := func(body map[string]any) int64 {
		code, out := e.do(t, "POST", "/api/v1/profiles", body, true)
		var r struct{ ID int64 }
		if code != 201 || json.Unmarshal(out, &r) != nil {
			t.Fatalf("add profile: %d %s", code, out)
		}
		return r.ID
	}
	guest := add(map[string]any{"name": "Ospite", "kind": "standard"})
	tv := pair(t, e, "TV", nil)
	code, tok := selectProfile(t, e, tv, guest, "")
	if code != 200 || tok == "" {
		t.Fatalf("select: %d", code)
	}
	if code, _ := e.do(t, "DELETE", "/api/v1/profiles/"+strconv.FormatInt(guest, 10), nil, true); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	boss := add(map[string]any{"name": "Capo", "kind": "admin", "pin": "8642"})
	if boss != guest {
		t.Skipf("id not reused (%d, %d): nothing to check", guest, boss)
	}
	if code, body := e.call(t, "GET", "/api/v1/me", nil, tv, tok); code == 200 && strings.Contains(string(body), `"admin":true`) {
		t.Fatalf("old token opens the new admin profile: %s", body)
	}
	if code, _ := e.call(t, "GET", "/api/v1/vpn", nil, tv, tok); code == 200 {
		t.Fatal("old token reaches admin routes")
	}

	// Locked device.
	kids := add(map[string]any{"name": "Bimbi", "kind": "kids"})
	pair(t, e, "Tablet", &kids)
	code, body := e.do(t, "DELETE", "/api/v1/profiles/"+strconv.FormatInt(kids, 10), nil, true)
	if code != 400 || !strings.Contains(string(body), "Tablet") {
		t.Fatalf("profile of a locked device deleted: %d %s", code, body)
	}
}
