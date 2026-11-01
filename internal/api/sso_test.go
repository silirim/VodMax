package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeIdP is a minimal OpenID Connect provider: one code per sign-in.
type fakeIdP struct {
	srv       *httptest.Server
	email     string
	verified  bool
	challenge string
	nonce     string
	redirect  string
	wrongAud  bool
}

func newFakeIdP(t *testing.T) *fakeIdP {
	f := &fakeIdP{verified: true}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/auth", "token_endpoint": f.srv.URL + "/token"})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if id != "vodmax" || secret != "s3cret" || r.Form.Get("code") != "good-code" || r.Form.Get("redirect_uri") != f.redirect ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		aud := "vodmax"
		if f.wrongAud {
			aud = "other"
		}
		claims, _ := json.Marshal(map[string]any{"iss": f.srv.URL, "sub": "u1", "aud": []string{aud}, "exp": time.Now().Add(time.Hour).Unix(),
			"nonce": f.nonce, "email": f.email, "email_verified": f.verified, "preferred_username": "giulia"})
		enc := base64.RawURLEncoding.EncodeToString
		json.NewEncoder(w).Encode(map[string]string{"id_token": enc([]byte(`{"alg":"RS256"}`)) + "." + enc(claims) + ".sig", "token_type": "Bearer"})
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// signIn follows the redirects as a browser would, up to the fragment.
func (f *fakeIdP) signIn(t *testing.T, e *env) url.Values {
	t.Helper()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(e.srv.URL + "/auth/sso/start")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	to, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || !strings.HasPrefix(to.String(), f.srv.URL+"/auth?") {
		frag, _ := url.ParseQuery(to.Fragment)
		return frag
	}
	q := to.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "vodmax" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("authorize request: %v", q)
	}
	f.challenge, f.nonce, f.redirect = q.Get("code_challenge"), q.Get("nonce"), q.Get("redirect_uri")
	resp, err = noFollow.Get(f.redirect + "?code=good-code&state=" + url.QueryEscape(q.Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	back, _ := url.Parse(resp.Header.Get("Location"))
	if back.Path != "/" {
		t.Fatalf("back to %s", back)
	}
	frag, _ := url.ParseQuery(back.Fragment)
	return frag
}

func TestSSOSignIn(t *testing.T) {
	e := setup(t, true)
	f := newFakeIdP(t)
	e.svc.SetSSOClient(f.srv.Client())
	if code, body := e.call(t, "GET", "/api/v1/sso", nil, "", ""); code != 200 || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("button off: %d %s", code, body)
	}
	e.do(t, "PATCH", "/api/v1/profiles/1", map[string]any{"pin": "9753"}, true)
	code, body := e.do(t, "POST", "/api/v1/profiles", map[string]any{"name": "Giulia", "kind": "standard", "pin": "2468"}, true)
	var giulia struct{ ID int64 }
	if code != 201 || json.Unmarshal(body, &giulia) != nil {
		t.Fatalf("profile: %d %s", code, body)
	}
	cfg := map[string]any{"enabled": true, "issuer": "http://idp.example", "client_id": "vodmax", "client_secret": "s3cret", "label": "Authelia",
		"accounts": []map[string]any{{"account": "Giulia@Example.org", "profile_id": giulia.ID}}}
	if code, body := e.do(t, "PUT", "/api/v1/settings/sso", cfg, true); code != 400 {
		t.Fatalf("http issuer accepted: %d %s", code, body)
	}
	cfg["issuer"] = f.srv.URL + "/"
	if code, body := e.do(t, "PUT", "/api/v1/settings/sso", cfg, true); code != 200 || strings.Contains(string(body), "s3cret") ||
		!strings.Contains(string(body), `"has_secret":true`) || !strings.Contains(string(body), "/auth/sso/callback") {
		t.Fatalf("settings: %d %s", code, body)
	}
	if code, body := e.call(t, "GET", "/api/v1/sso", nil, "", ""); code != 200 || !strings.Contains(string(body), `"label":"Authelia"`) {
		t.Fatalf("button: %d %s", code, body)
	}

	// A linked account enters, on a device locked to its profile.
	f.email = "giulia@example.org"
	frag := f.signIn(t, e)
	if frag.Get("sso") == "" {
		t.Fatalf("no code: %v", frag)
	}
	code, body = e.call(t, "POST", "/api/v1/sso/claim", map[string]string{"code": frag.Get("sso")}, "", "")
	var got struct{ Token, Profile string }
	if code != 200 || json.Unmarshal(body, &got) != nil || got.Token == "" || got.Profile == "" {
		t.Fatalf("claim: %d %s", code, body)
	}
	if code, _ := e.call(t, "POST", "/api/v1/sso/claim", map[string]string{"code": frag.Get("sso")}, "", ""); code != 404 {
		t.Fatalf("claimed twice: %d", code)
	}
	if code, body := e.call(t, "GET", "/api/v1/me", nil, got.Token, got.Profile); code != 200 || !strings.Contains(string(body), "Giulia") {
		t.Fatalf("me: %d %s", code, body)
	}
	if code, _ := e.call(t, "GET", "/api/v1/settings/output", nil, got.Token, got.Profile); code != 403 {
		t.Fatalf("not an admin: %d", code)
	}
	_, body = e.do(t, "GET", "/api/v1/devices", nil, true)
	if !strings.Contains(string(body), "giulia@example.org") || !strings.Contains(string(body), `"profile_lock":`+jsonInt(giulia.ID)) {
		t.Fatalf("device: %s", body)
	}

	// Unverified e-mail: the username is tried, which is not linked.
	f.verified = false
	if frag := f.signIn(t, e); frag.Get("sso") != "" || !strings.Contains(frag.Get("sso_error"), "non è collegato") {
		t.Fatalf("unverified e-mail entered: %v", frag)
	}
	f.verified = true
	f.wrongAud = true
	if frag := f.signIn(t, e); frag.Get("sso") != "" || frag.Get("sso_error") == "" {
		t.Fatalf("token for another client accepted: %v", frag)
	}
	f.wrongAud = false
	// A state nobody started.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, _ := noFollow.Get(e.srv.URL + "/auth/sso/callback?code=good-code&state=forged")
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "sso_error") {
		t.Fatalf("forged state: %s", loc)
	}
	// Turned off: no sign-in.
	cfg["enabled"] = false
	delete(cfg, "client_secret")
	e.do(t, "PUT", "/api/v1/settings/sso", cfg, true)
	if frag := f.signIn(t, e); frag.Get("sso_error") == "" {
		t.Fatalf("off but entered: %v", frag)
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

// A refused form does not keep half of it: the secret is not saved.
func TestSSORefusedFormSavesNothing(t *testing.T) {
	e := setup(t, true)
	code, _ := e.do(t, "PUT", "/api/v1/settings/sso", map[string]any{"enabled": true, "issuer": "https://login.example",
		"client_id": "vodmax", "client_secret": "segreto"}, true)
	if code != 400 {
		t.Fatalf("form without accounts: %d", code)
	}
	_, body := e.do(t, "GET", "/api/v1/settings/sso", nil, true)
	if strings.Contains(string(body), `"has_secret":true`) {
		t.Fatalf("secret saved by a refused form: %s", body)
	}
}
