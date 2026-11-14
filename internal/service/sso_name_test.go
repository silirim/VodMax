package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A device paired through the home login is named after the account; a
// long name in a non-Latin script must be shortened on a character
// boundary, to at most 60 characters, keeping as many as fit.
func TestSSODeviceNameLongUnicodeAccount(t *testing.T) {
	s, _ := localTestService(t)
	ctx := context.Background()
	user := "a" + strings.Repeat("ж", 69) // 70 characters, 139 bytes
	var srv *httptest.Server
	var nonce string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "authorization_endpoint": srv.URL + "/auth", "token_endpoint": srv.URL + "/token"})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		claims, _ := json.Marshal(map[string]any{"iss": srv.URL, "sub": "u1", "aud": "vodmax", "exp": time.Now().Add(time.Hour).Unix(),
			"nonce": nonce, "preferred_username": user})
		enc := base64.RawURLEncoding.EncodeToString
		json.NewEncoder(w).Encode(map[string]string{"id_token": enc([]byte(`{"alg":"none"}`)) + "." + enc(claims) + ".x"})
	})
	srv = httptest.NewTLSServer(mux)
	defer srv.Close()
	s.SetSSOClient(srv.Client())

	profiles, err := s.st.Profiles(ctx)
	if err != nil || len(profiles) == 0 {
		t.Fatal(profiles, err)
	}
	secret := "s3cret"
	if err := s.SetSSO(ctx, SSOInput{Enabled: true, Issuer: srv.URL, ClientID: "vodmax", Secret: &secret,
		Accounts: []SSOAccount{{Account: user, ProfileID: profiles[0].ID}}}); err != nil {
		t.Fatal(err)
	}
	to, err := s.SSOStart(ctx, "http://tv/cb", "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to)
	nonce = u.Query().Get("nonce")
	once, err := s.SSOFinish(ctx, u.Query().Get("state"), "code")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SSOClaim(ctx, once); err != nil {
		t.Fatal(err)
	}
	devices, err := s.st.Devices(ctx)
	if err != nil || len(devices) != 1 {
		t.Fatal(devices, err)
	}
	name := devices[0].Name
	if !utf8.ValidString(name) {
		t.Fatalf("device name is not valid UTF-8: %q", name)
	}
	if n := utf8.RuneCountInString(name); n != 60 {
		t.Fatalf("device name has %d characters, want 60: %q", n, name)
	}
	if !strings.HasPrefix(name, "Accesso aжж") {
		t.Fatalf("device name %q", name)
	}
}
