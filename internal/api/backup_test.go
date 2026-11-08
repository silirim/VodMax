package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBackupRestoreOnNewInstallation(t *testing.T) {
	e, p := tvSetup(t)
	// User data: a favourite channel, film and episode progress, rules, a
	// category override, the schedule.
	tv := channels(t, e, "?section=tv")
	e.do(t, "PUT", "/api/v1/tv/favorites/"+strconv.FormatInt(tv[1].ID, 10), map[string]bool{"favorite": true}, true)
	film := strconv.FormatInt(findItem(t, e, "movie", "1408"), 10)
	e.do(t, "PUT", "/api/v1/progress/movie/"+film, map[string]int{"position": 1800, "duration": 6000}, true)
	series := strconv.FormatInt(findItem(t, e, "series", "twin"), 10)
	_, body := e.do(t, "GET", "/api/v1/series/"+series+"/episodes", nil, true)
	ep := strings.Split(strings.Split(string(body), `"id":`)[2], ",")[0]
	e.do(t, "PUT", "/api/v1/progress/episode/"+ep, map[string]int{"position": 600, "duration": 2800}, true)
	if code, _ := e.do(t, "PUT", "/api/v1/subscriptions/1/rules", map[string]any{"movie_min_year": 1990}, true); code != 200 {
		t.Fatal("rules")
	}
	e.do(t, "PUT", "/api/v1/settings/schedule", map[string]int{"catalog_hour": 2, "guide_hours": 3}, true)

	if code, _ := e.do(t, "POST", "/api/v1/backup", map[string]string{"password": "corta"}, true); code != 400 {
		t.Fatal("short password accepted")
	}
	code, file := e.do(t, "POST", "/api/v1/backup", map[string]string{"password": "una password lunga"}, true)
	if code != 200 || !bytes.HasPrefix(file, []byte("VODMAXB1P")) {
		t.Fatalf("backup: %d %q", code, file[:min(20, len(file))])
	}
	if bytes.Contains(file, []byte(p.URL)) || bytes.Contains(file, []byte("PrivateKey")) {
		t.Fatal("backup not encrypted")
	}

	// A new installation (other secret key, empty database).
	n := setup(t, true)
	if code, _ := restore(t, n, file, "sbagliata!!"); code != 400 {
		t.Fatalf("wrong password: %d", code)
	}
	if code, body := restore(t, n, file, "una password lunga"); code != 204 {
		t.Fatalf("restore: %d %s", code, body)
	}
	// The catalog comes back in the background, then progress is attached.
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, body = n.do(t, "GET", "/api/v1/continue", nil, true)
		if strings.Contains(string(body), "1408") && strings.Contains(string(body), "Twin Peaks") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("progress not restored: %s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(string(body), `"position":1800`) || !strings.Contains(string(body), `"position":600`) {
		t.Fatalf("positions: %s", body)
	}
	if fav := channels(t, n, "?list=favorites"); len(fav) != 1 || fav[0].Name != tv[1].Name {
		t.Fatalf("favourites: %+v", fav)
	}
	if _, body = n.do(t, "GET", "/api/v1/subscriptions/1/rules", nil, true); !strings.Contains(string(body), `"movie_min_year":1990`) {
		t.Fatalf("rules: %s", body)
	}
	if _, body = n.do(t, "GET", "/api/v1/settings/schedule", nil, true); !strings.Contains(string(body), `"catalog_hour":2`) {
		t.Fatalf("schedule: %s", body)
	}
	// The provider password works (re-encrypted with the new key). The
	// restore starts a download of the catalogs by itself: wait for it.
	code, body = n.do(t, "POST", "/api/v1/subscriptions/1/sync?wait=true", nil, true)
	for deadline := time.Now().Add(30 * time.Second); code == 409 && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		code, body = n.do(t, "POST", "/api/v1/subscriptions/1/sync?wait=true", nil, true)
	}
	if code != 200 {
		t.Fatalf("sync after restore: %d %s", code, body)
	}
}

func TestAutomaticBackups(t *testing.T) {
	e, _ := tvSetup(t)
	if _, body := e.do(t, "GET", "/api/v1/backups", nil, true); string(bytes.TrimSpace(body)) != "[]" {
		t.Fatalf("list: %s", body)
	}
	code, body := e.do(t, "POST", "/api/v1/backups", nil, true)
	if code != 201 {
		t.Fatalf("make: %d %s", code, body)
	}
	name := strings.Split(strings.Split(string(body), `"name":"`)[1], `"`)[0]
	if code, _ := e.do(t, "GET", "/api/v1/backups/..%2Fsecret.key", nil, true); code != 404 {
		t.Fatalf("path traversal: %d", code)
	}
	e.do(t, "PUT", "/api/v1/subscriptions/1/rules", map[string]any{"movie_min_year": 2020}, true)
	if code, body := e.do(t, "POST", "/api/v1/backups/"+name+"/restore", nil, true); code != 204 {
		t.Fatalf("restore: %d %s", code, body)
	}
	if _, body := e.do(t, "GET", "/api/v1/subscriptions/1/rules", nil, true); strings.Contains(string(body), "2020") {
		t.Fatalf("rules not restored: %s", body)
	}
	// A copy made on another installation cannot be opened here.
	_, file := e.do(t, "GET", "/api/v1/backups/"+name, nil, true)
	other := setup(t, true)
	if code, _ := restore(t, other, file, ""); code != 400 {
		t.Fatalf("foreign automatic copy: %d", code)
	}
}

func restore(t *testing.T, e *env, file []byte, password string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/restore", bytes.NewReader(file))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Backup-Password", password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// A backup restored on a new installation brings back the profiles, with
// their PINs and the kids profile, and what each took off "continue
// watching".
func TestBackupKeepsProfiles(t *testing.T) {
	e, _ := tvSetup(t)
	e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": "9753"}, true)
	if code, body := e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "Bimbi", "kind": "kids"}, true); code != 201 {
		t.Fatalf("kids profile: %d %s", code, body)
	}
	e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "Marco", "kind": "standard", "pin": "2468"}, true)
	film := strconv.FormatInt(findItem(t, e, "movie", "1408"), 10)
	e.do(t, "PUT", "/api/v1/progress/movie/"+film, map[string]int{"position": 600, "duration": 6000}, true)
	time.Sleep(1100 * time.Millisecond)
	if code, _ := e.do(t, "DELETE", "/api/v1/continue/"+film, nil, true); code != 204 {
		t.Fatal("hide from continue")
	}
	_, file := e.do(t, "POST", "/api/v1/backup", map[string]string{"password": "una password lunga"}, true)

	n, _ := tvSetup(t)
	if code, body := restore(t, n, file, "una password lunga"); code != 204 {
		t.Fatalf("restore: %d %s", code, body)
	}
	_, body := n.do(t, "GET", "/api/v1/profiles", nil, true)
	var profiles []struct {
		ID     int64
		Name   string
		Kind   string
		HasPIN bool `json:"has_pin"`
	}
	json.Unmarshal(body, &profiles)
	got := fmt.Sprint(profiles)
	if len(profiles) != 3 || got != "[{1 Principale admin true} {2 Bimbi kids false} {3 Marco standard true}]" {
		t.Fatalf("profiles after restore: %s", got)
	}
	device := pair(t, n, "Salotto", nil)
	if code, _ := selectProfile(t, n, device, 3, "1111"); code == 200 {
		t.Fatal("wrong PIN accepted after restore")
	}
	if code, _ := selectProfile(t, n, device, 3, "2468"); code != 200 {
		t.Fatalf("PIN of the backup: %d", code)
	}
	// The film stays off "continue watching" once the catalog is back.
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, body = n.do(t, "GET", "/api/v1/progress/movie/"+strconv.FormatInt(findItem(t, n, "movie", "1408"), 10), nil, true)
		if strings.Contains(string(body), `"position":600`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("progress not restored: %s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, body := n.do(t, "GET", "/api/v1/continue", nil, true); strings.Contains(string(body), "1408") {
		t.Fatalf("hidden title back in continue: %s", body)
	}
}

// Restoring yesterday's copy keeps the recordings made since then (their
// files are still there), and the restored ones still play.
func TestRestoreKeepsLaterRecordings(t *testing.T) {
	e, _ := tvSetup(t)
	recordFor(t, e, 2, "Prima")
	time.Sleep(3500 * time.Millisecond)
	_, file := e.do(t, "POST", "/api/v1/backup", map[string]string{"password": "una password lunga"}, true)
	recordFor(t, e, 2, "Dopo")
	time.Sleep(3500 * time.Millisecond)
	if code, body := restore(t, e, file, "una password lunga"); code != 204 {
		t.Fatalf("restore: %d %s", code, body)
	}
	l := recRows(t, e)
	if len(l) != 2 || len(recFiles(t, e)) != 2 {
		t.Fatalf("after the restore: %+v, files %v", l, recFiles(t, e))
	}
	for _, r := range l {
		_, body := e.do(t, "POST", fmt.Sprintf("/api/v1/recordings/%d/ticket", r.ID), nil, true)
		var tk struct{ URL string }
		json.Unmarshal(body, &tk)
		resp, err := http.Get(e.srv.URL + tk.URL)
		if err != nil || resp.StatusCode != 200 || r.Status != "completed" {
			t.Fatalf("%s after the restore: %v %v %+v", r.Title, err, resp, r)
		}
		resp.Body.Close()
	}
}

// The client secret of the home account travels in the backup like the
// TMDB key: on a new installation the settings open, the secret is there,
// and a new one can be saved.
func TestRestoreReSealsTheHomeAccountSecret(t *testing.T) {
	e := setup(t, true)
	f := newFakeIdP(t)
	e.svc.SetSSOClient(f.srv.Client())
	cfg := map[string]any{"enabled": true, "issuer": f.srv.URL + "/", "client_id": "vodmax", "client_secret": "s3cret", "label": "Authelia",
		"accounts": []map[string]any{{"account": "a@example.org", "profile_id": 1}}}
	if code, body := e.do(t, "PUT", "/api/v1/settings/sso", cfg, true); code != 200 {
		t.Fatalf("sso: %d %s", code, body)
	}
	_, file := e.do(t, "POST", "/api/v1/backup", map[string]string{"password": "una password lunga"}, true)
	n := setup(t, true)
	n.svc.SetSSOClient(f.srv.Client())
	if code, body := restore(t, n, file, "una password lunga"); code != 204 {
		t.Fatalf("restore: %d %s", code, body)
	}
	if code, body := n.do(t, "GET", "/api/v1/settings/sso", nil, true); code != 200 || !strings.Contains(string(body), `"has_secret":true`) {
		t.Fatalf("settings after restore: %d %s", code, body)
	}
	cfg["client_secret"] = "nuovo"
	if code, body := n.do(t, "PUT", "/api/v1/settings/sso", cfg, true); code != 200 {
		t.Fatalf("new secret after restore: %d %s", code, body)
	}
}

// Restoring an older copy on the same server: a device locked to a profile
// follows that profile (by name and kind) to its restored id, and one whose
// profile is not in the copy is unpaired, never left free or moved onto
// another profile that took the same id.
func TestRestoreKeepsDeviceLocks(t *testing.T) {
	e, _ := tvSetup(t)
	e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": "9753"}, true)
	addProfile := func(name, kind string) int64 {
		code, out := e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": name, "kind": kind}, true)
		var r struct{ ID int64 }
		if code != 201 || json.Unmarshal(out, &r) != nil {
			t.Fatalf("add profile: %d %s", code, out)
		}
		return r.ID
	}
	papa, bimbi := addProfile("Papà", "standard"), addProfile("Bimbi", "kids")
	_, file := e.do(t, "POST", "/api/v1/backup", map[string]string{"password": "una password lunga"}, true)
	for _, id := range []int64{papa, bimbi} {
		if code, body := e.do(t, "DELETE", "/api/v1/profiles/"+strconv.FormatInt(id, 10), nil, true); code != 204 {
			t.Fatalf("delete profile: %d %s", code, body)
		}
	}
	// Ids are reused: the new Bimbi takes Papà's old id.
	newBimbi, nonna := addProfile("Bimbi", "kids"), addProfile("Nonna", "kids")
	if newBimbi != papa {
		t.Fatalf("setup: id %d not reused (%d)", newBimbi, papa)
	}
	tablet := pair(t, e, "Tablet", &newBimbi)
	other := pair(t, e, "TV nonna", &nonna)

	if code, body := restore(t, e, file, "una password lunga"); code != 204 {
		t.Fatalf("restore: %d %s", code, body)
	}
	code, me := e.call(t, "GET", "/api/v1/me", nil, tablet, "")
	var who struct {
		Device struct {
			ProfileLock *int64 `json:"profile_lock"`
		}
		Profile *struct{ Name, Kind string }
	}
	if code != 200 || json.Unmarshal(me, &who) != nil || who.Device.ProfileLock == nil || *who.Device.ProfileLock != bimbi {
		t.Fatalf("tablet after the restore: %d %s", code, me)
	}
	if who.Profile != nil && who.Profile.Kind != "kids" {
		t.Fatalf("tablet on an adult profile: %s", me)
	}
	if code, body := e.call(t, "GET", "/api/v1/me", nil, other, ""); code != 401 {
		t.Fatalf("device locked to a profile not in the copy still paired: %d %s", code, body)
	}
}
