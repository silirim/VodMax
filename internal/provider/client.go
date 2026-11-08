// Package provider builds the only HTTP client allowed to talk to playlist
// providers. It dials exclusively through the internal VPN.
package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/silirim/vodmax/internal/vpn"
)

// Dialer is satisfied by *vpn.Tunnel. Nothing else should implement it
// outside tests.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	Up() bool
}

// ErrStalled: the provider stopped sending in the middle of an answer.
var ErrStalled = errors.New("il provider si è fermato a metà risposta (nessun dato per un minuto)")

// IdleTimeout ends a read that receives nothing for this long: a panel
// that stalls in the middle of a body would otherwise hold a sync until
// the client's overall timeout (10 minutes). A variable for tests.
var IdleTimeout = time.Minute

// NewHTTPClient returns a client whose every connection goes through d.
// There is deliberately no option to use the normal network or a proxy.
func NewHTTPClient(d Dialer, timeout time.Duration) *http.Client {
	idle := IdleTimeout
	transport := &http.Transport{
		// Environment proxies (HTTP_PROXY, ...) would bypass the tunnel.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if d == nil || !d.Up() {
				return nil, vpn.ErrNotConnected
			}
			c, err := d.DialContext(ctx, network, address)
			if err != nil || timeout == 0 {
				// Streams and recordings (no overall timeout) keep their
				// connection: over HTTP/2 a paused player stops all reads.
				return c, err
			}
			return idleConn{c, idle}, nil
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

// idleConn fails a read that waits longer than idle. The deadline starts
// with each read; only clients with an overall timeout (bounded answers,
// not streams) get it.
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c idleConn) Read(p []byte) (int, error) {
	c.SetReadDeadline(time.Now().Add(c.idle))
	n, err := c.Conn.Read(p)
	// The WireGuard tunnel's timeout is not os.ErrDeadlineExceeded.
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		err = ErrStalled
	}
	return n, err
}

// Redact strips the query string and user info from URLs in err, since
// provider URLs carry credentials (Xtream puts the password in the query
// and in the path, so the path is dropped too).
func Redact(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		safe := "(url del provider)"
		if u, perr := url.Parse(uerr.URL); perr == nil {
			safe = u.Scheme + "://" + u.Host + "/…"
		}
		return &url.Error{Op: uerr.Op, URL: safe, Err: uerr.Err}
	}
	return err
}
