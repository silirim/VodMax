package api

import (
	"errors"
	"net"
	"net/http"
	"net/url"

	"github.com/silirim/vodmax/internal/service"
)

// Sign-in with an account of the house (OpenID Connect).

func ssoRedirect(r *http.Request) string { return baseURL(r) + "/auth/sso/callback" }

func (a *api) ssoButton(w http.ResponseWriter, r *http.Request) {
	on, label := a.svc.SSOButton(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"enabled": on, "label": label})
}

// ssoBack returns to the interface: a one-time code or an error in the
// fragment, which never reaches a server log.
func (a *api) ssoBack(w http.ResponseWriter, r *http.Request, key, value string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "/#"+key+"="+url.QueryEscape(value), http.StatusFound)
}

func (a *api) ssoStart(w http.ResponseWriter, r *http.Request) {
	client, _, _ := net.SplitHostPort(r.RemoteAddr)
	to, err := a.svc.SSOStart(r.Context(), ssoRedirect(r), client)
	if err != nil {
		a.log.Warn("accesso con account esterno", "err", err)
		a.ssoBack(w, r, "sso_error", service.Localize(service.LangOf(r.Context()), err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusFound)
}

func (a *api) ssoCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		a.ssoBack(w, r, "sso_error", service.Localize(service.LangOf(r.Context()), service.ErrSSOLogin)+" ("+e+")")
		return
	}
	once, err := a.svc.SSOFinish(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		if !errors.Is(err, service.ErrSSOAccount) && !errors.Is(err, service.ErrSSOLogin) {
			a.log.Warn("accesso con account esterno", "err", err)
		}
		a.ssoBack(w, r, "sso_error", service.Localize(service.LangOf(r.Context()), err))
		return
	}
	a.ssoBack(w, r, "sso", once)
}

func (a *api) ssoClaim(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	token, profile, err := a.svc.SSOClaim(in.Code)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": service.Localize(service.LangOf(r.Context()), err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "profile": profile})
}

func (a *api) ssoView(w http.ResponseWriter, r *http.Request) {
	o, secret, err := a.svc.SSOSettings(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": o.Enabled, "issuer": o.Issuer, "client_id": o.ClientID, "label": o.Label,
		"accounts": o.Accounts, "has_secret": secret, "redirect_uri": ssoRedirect(r)})
}

func (a *api) putSSO(w http.ResponseWriter, r *http.Request) {
	var in service.SSOInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetSSO(r.Context(), in); err != nil {
		a.fail(w, r, err)
		return
	}
	a.ssoView(w, r)
}
