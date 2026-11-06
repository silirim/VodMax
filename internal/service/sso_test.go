package service

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The home login trusts an e-mail only when the account service says it is
// verified, and a username only for accounts written as a username.
func TestSSOMatch(t *testing.T) {
	yes, no := true, false
	accounts := []SSOAccount{{Account: "papa@casa.it", ProfileID: 1}, {Account: "giulia", ProfileID: 2}}
	cases := []struct {
		c    ssoClaims
		want int64
	}{
		{ssoClaims{Email: "Papa@casa.it", EmailVerified: &yes}, 1},
		{ssoClaims{Email: "papa@casa.it", EmailVerified: &no}, 0},
		{ssoClaims{Email: "papa@casa.it"}, 0},    // not said: not verified
		{ssoClaims{Username: "papa@casa.it"}, 0}, // a username that looks like the e-mail
		{ssoClaims{Username: "Giulia", Email: "x@y.it"}, 2},
	}
	for _, c := range cases {
		if got, _ := ssoMatch(accounts, c.c); got != c.want {
			t.Errorf("%+v → profile %d, want %d", c.c, got, c.want)
		}
	}
}

// One address cannot use up the sign-ins (the start page needs no login).
func TestSSOStartPerClient(t *testing.T) {
	s, _ := localTestService(t)
	ctx := context.Background()
	p, err := s.st.Profiles(ctx)
	if err != nil || len(p) == 0 {
		t.Fatal(p, err)
	}
	secret := "x"
	if err := s.SetSSO(ctx, SSOInput{Enabled: true, Issuer: "https://login.example", ClientID: "vm", Secret: &secret,
		Accounts: []SSOAccount{{Account: "a@b.it", ProfileID: p[0].ID}}, Addresses: []string{"http://tv"}}); err != nil {
		t.Fatal(err)
	}
	s.sso.disc = &ssoDiscovery{configured: "https://login.example", fetched: time.Now(), AuthEndpoint: "https://login.example/auth"}
	for i := range ssoLoginsPerClient {
		if _, err := s.SSOStart(ctx, "http://tv", "10.0.0.5"); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := s.SSOStart(ctx, "http://tv", "10.0.0.5"); !errors.Is(err, ErrSSOBusy) {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := s.SSOStart(ctx, "http://tv", "10.0.0.6"); err != nil {
		t.Fatalf("another address: %v", err)
	}
}

// A sign-in starts only from the addresses of VodMax the admin listed;
// settings saved before the list existed keep working from any address.
func TestSSOStartAddresses(t *testing.T) {
	s, _ := localTestService(t)
	ctx := context.Background()
	p, err := s.st.Profiles(ctx)
	if err != nil || len(p) == 0 {
		t.Fatal(p, err)
	}
	secret := "x"
	in := SSOInput{Enabled: true, Issuer: "https://login.example", ClientID: "vm", Secret: &secret,
		Accounts: []SSOAccount{{Account: "a@b.it", ProfileID: p[0].ID}}, Addresses: []string{" HTTP://Casa.example:8095/ "}}
	if err := s.SetSSO(ctx, in); err != nil {
		t.Fatal(err)
	}
	s.sso.disc = &ssoDiscovery{configured: "https://login.example", fetched: time.Now(), AuthEndpoint: "https://login.example/auth"}
	if to, err := s.SSOStart(ctx, "http://casa.example:8095", "10.0.0.5"); err != nil || !strings.Contains(to, url.QueryEscape("http://casa.example:8095/auth/sso/callback")) {
		t.Fatalf("listed address: %s %v", to, err)
	}
	if _, err := s.SSOStart(ctx, "http://attacker.example", "10.0.0.5"); !errors.Is(err, ErrSSOAddress) {
		t.Fatalf("unlisted address: %v", err)
	}
	if on, _ := s.SSOButton(ctx, "http://attacker.example"); on {
		t.Fatal("button shown on an unlisted address")
	}
	// Saved by an older version: no list.
	o, _, _ := s.SSOSettings(ctx)
	o.Addresses = nil
	if err := s.st.SetSetting(ctx, ssoKey, o); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SSOStart(ctx, "http://tunnel.example", "10.0.0.5"); err != nil {
		t.Fatalf("settings without a list: %v", err)
	}
}
