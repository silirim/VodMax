// Package stalker is a client for Stalker / Ministra portals, the
// middleware of MAG set-top boxes: the box is identified by its MAC
// address, gets a token with a handshake and asks a fresh link for every
// channel it plays. Only live channels are read for now.
package stalker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/silirim/vodmax/internal/playlist/xtream"
	"github.com/silirim/vodmax/internal/provider"
)

const maxResponse = 128 << 20

var (
	macFormat = regexp.MustCompile(`^[0-9A-F]{2}(:[0-9A-F]{2}){5}$`)
	// ErrAuth: the portal does not know this MAC, or the token expired.
	ErrAuth = errors.New("il portale non riconosce questo indirizzo MAC (o l'abbonamento è scaduto)")
)

// NormalizeMAC returns the MAC as 00:1A:79:AA:BB:CC, or an error.
func NormalizeMAC(mac string) (string, error) {
	m := strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(mac, "-", ":")))
	if !macFormat.MatchString(m) {
		return "", errors.New("indirizzo MAC non valido (atteso 00:1A:79:XX:XX:XX)")
	}
	return m, nil
}

// Client talks to one portal as one set-top box. HTTP must come from
// provider.NewHTTPClient so that every request uses the internal VPN.
type Client struct {
	api  string // .../portal.php or .../server/load.php
	ref  string
	mac  string
	http *http.Client

	mu    sync.Mutex
	token string
}

// New accepts the portal address as people find it ("http://host:8080/c/",
// "http://host/stalker_portal/c/", or the API address itself).
func New(portal, mac string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(portal))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("indirizzo del portale non valido (atteso http:// o https://)")
	}
	m, err := NormalizeMAC(mac)
	if err != nil {
		return nil, err
	}
	u.RawQuery, u.Fragment = "", ""
	p := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(p, ".php"):
	case strings.HasSuffix(p, "/c"):
		p = strings.TrimSuffix(p, "/c")
		if strings.HasSuffix(p, "stalker_portal") {
			p += "/server/load.php"
		} else {
			p += "/portal.php"
		}
	default:
		p += "/portal.php"
	}
	ref := *u
	ref.Path = strings.TrimSuffix(strings.TrimSuffix(p, "/portal.php"), "/server/load.php") + "/c/"
	u.Path = p
	return &Client{api: u.String(), ref: ref.String(), mac: m, http: httpClient}, nil
}

// Token and SetToken keep the session between clients (a client is made
// for each use, on the VPN connection of the moment).
func (c *Client) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *Client) SetToken(token string) {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
}

// call makes one API request; js is decoded from the "js" member.
func (c *Client) call(ctx context.Context, q url.Values, js any) error {
	q.Set("JsHttpRequest", "1-xml")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (QtEmbedded; U; Linux; C) AppleWebKit/533.3 (KHTML, like Gecko) MAG200 stbapp ver: 2 rev: 250 Safari/533.3")
	req.Header.Set("X-User-Agent", "Model: MAG250; Link: WiFi")
	req.Header.Set("Referer", c.ref)
	req.Header.Set("Cookie", "mac="+url.QueryEscape(c.mac)+"; stb_lang=en; timezone=Europe%2FRome")
	c.mu.Lock()
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	c.mu.Unlock()
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("portale: %w", provider.Redact(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("portale: risposta HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("portale: %w", provider.Redact(err))
	}
	// An expired or refused token answers with plain text.
	if s := strings.TrimSpace(string(body)); strings.HasPrefix(s, "Authorization failed") || strings.HasPrefix(s, "Access denied") {
		return ErrAuth
	}
	var env struct {
		JS json.RawMessage `json:"js"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("portale: risposta non valida: %w", err)
	}
	// No answer at all is how many portals refuse a box.
	if len(env.JS) == 0 || string(env.JS) == "null" || string(env.JS) == "false" {
		return ErrAuth
	}
	if js == nil {
		return nil
	}
	if err := json.Unmarshal(env.JS, js); err != nil {
		return fmt.Errorf("portale: risposta non valida: %w", err)
	}
	return nil
}

// Login makes the handshake and registers the box (get_profile).
func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
	var hs struct {
		Token string `json:"token"`
	}
	if err := c.call(ctx, url.Values{"type": {"stb"}, "action": {"handshake"}, "token": {""}}, &hs); err != nil {
		return err
	}
	if hs.Token == "" {
		return ErrAuth
	}
	c.mu.Lock()
	c.token = hs.Token
	c.mu.Unlock()
	var profile struct {
		ID xtream.Flex `json:"id"`
	}
	if err := c.call(ctx, url.Values{"type": {"stb"}, "action": {"get_profile"}, "hd": {"1"}, "stb_type": {"MAG250"},
		"ver":       {"ImageDescription: 0.2.18-r23-250; PORTAL version: 5.6.1; API Version: JS API version: 343"},
		"num_banks": {"2"}, "image_version": {"218"}, "auth_second_step": {"1"}}, &profile); err != nil {
		return err
	}
	return nil
}

// ensure logs in when there is no token yet.
func (c *Client) ensure(ctx context.Context) error {
	c.mu.Lock()
	ok := c.token != ""
	c.mu.Unlock()
	if ok {
		return nil
	}
	return c.Login(ctx)
}

// retry runs f, logging in again once if the token was refused.
func (c *Client) retry(ctx context.Context, f func() error) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	err := f()
	if errors.Is(err, ErrAuth) {
		if err := c.Login(ctx); err != nil {
			return err
		}
		err = f()
	}
	return err
}

// Genre is a channel category. Fields are Flex: portals are as loose
// about JSON types as Xtream panels.
type Genre struct {
	ID    xtream.Flex `json:"id"`
	Title xtream.Flex `json:"title"`
}

// Channel is a live channel; Cmd is what create_link needs.
type Channel struct {
	ID      xtream.Flex `json:"id"`
	Name    xtream.Flex `json:"name"`
	Number  xtream.Flex `json:"number"`
	Cmd     xtream.Flex `json:"cmd"`
	Logo    xtream.Flex `json:"logo"`
	GenreID xtream.Flex `json:"tv_genre_id"`
	XMLTVID xtream.Flex `json:"xmltv_id"`
}

func (c *Client) Genres(ctx context.Context) ([]Genre, error) {
	var out []Genre
	err := c.retry(ctx, func() error { return c.call(ctx, url.Values{"type": {"itv"}, "action": {"get_genres"}}, &out) })
	return out, err
}

// Channels lists every channel, leaving out the entries that do not
// decode: one malformed channel must not stop the portal from updating.
func (c *Client) Channels(ctx context.Context) ([]Channel, error) {
	var out struct {
		Data []json.RawMessage `json:"data"`
	}
	err := c.retry(ctx, func() error { return c.call(ctx, url.Values{"type": {"itv"}, "action": {"get_all_channels"}}, &out) })
	list := make([]Channel, 0, len(out.Data))
	for _, raw := range out.Data {
		var ch Channel
		if json.Unmarshal(raw, &ch) == nil {
			list = append(list, ch)
		}
	}
	return list, err
}

// Link asks the address to play a channel now (it usually carries a
// short-lived token).
func (c *Client) Link(ctx context.Context, cmd string) (string, error) {
	var out struct {
		Cmd string `json:"cmd"`
	}
	err := c.retry(ctx, func() error {
		return c.call(ctx, url.Values{"type": {"itv"}, "action": {"create_link"}, "cmd": {cmd}, "forced_storage": {"undefined"},
			"disable_ad": {"0"}, "download": {"0"}}, &out)
	})
	if err != nil {
		return "", err
	}
	link := StreamURL(out.Cmd)
	if link == "" {
		link = StreamURL(cmd) // portals with fixed addresses answer an empty cmd
	}
	if u, err := url.Parse(link); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("portale: indirizzo del canale non valido")
	}
	return link, nil
}

// StreamURL drops the player prefix of a cmd ("ffmpeg http://...").
func StreamURL(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if i := strings.LastIndex(cmd, " "); i >= 0 {
		cmd = cmd[i+1:]
	}
	if !strings.HasPrefix(cmd, "http://") && !strings.HasPrefix(cmd, "https://") {
		return ""
	}
	return cmd
}
