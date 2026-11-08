package provider

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silirim/vodmax/internal/vpn"
)

type upDialer struct{}

func (upDialer) Up() bool { return true }
func (upDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

// tunnelDialer fails a read past its deadline as the WireGuard tunnel
// (gvisor gonet) does: a timeout error that is not os.ErrDeadlineExceeded.
type tunnelDialer struct{ upDialer }

type tunnelConn struct{ net.Conn }

type gonetTimeout struct{}

func (gonetTimeout) Error() string   { return "i/o timeout" }
func (gonetTimeout) Timeout() bool   { return true }
func (gonetTimeout) Temporary() bool { return true }

func (c tunnelConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		err = &net.OpError{Op: "read", Net: "tcp", Err: gonetTimeout{}}
	}
	return n, err
}

func (d tunnelDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c, err := d.upDialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return tunnelConn{c}, nil
}

// A provider that stops sending in the middle of a body fails the read
// after IdleTimeout, well before the client's overall timeout, through
// the tunnel too; a client without an overall timeout (a stream) waits.
func TestStalledBodyFails(t *testing.T) {
	defer func(old time.Duration) { IdleTimeout = old }(IdleTimeout)
	IdleTimeout = 200 * time.Millisecond
	for _, c := range []struct {
		name    string
		d       Dialer
		timeout time.Duration
	}{{"rete", upDialer{}, time.Minute}, {"tunnel", tunnelDialer{}, time.Minute}, {"stream", upDialer{}, 0}} {
		t.Run(c.name, func(t *testing.T) {
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`[{"name":"uno"},`))
				w.(http.Flusher).Flush()
				<-release
				w.Write([]byte(`{"name":"due"}]`))
			}))
			defer srv.Close()
			resp, err := NewHTTPClient(c.d, c.timeout).Get(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if c.timeout == 0 {
				time.AfterFunc(3*IdleTimeout, func() { close(release) })
				if body, err := io.ReadAll(resp.Body); err != nil || string(body) != `[{"name":"uno"},{"name":"due"}]` {
					t.Fatalf("stream read %q, %v", body, err)
				}
				return
			}
			defer close(release)
			start := time.Now()
			body, err := io.ReadAll(resp.Body)
			if !errors.Is(err, ErrStalled) || string(body) != `[{"name":"uno"},` {
				t.Fatalf("read %q, %v", body, err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("stalled body failed after %v", d)
			}
		})
	}
}

type downDialer struct{ dials atomic.Int32 }

func (d *downDialer) Up() bool { return false }
func (d *downDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.dials.Add(1)
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestNoTrafficWithoutVPN(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	d := &downDialer{}
	for _, client := range []*http.Client{NewHTTPClient(d, 5*time.Second), NewHTTPClient(nil, 5*time.Second)} {
		_, err := client.Get(srv.URL + "/player_api.php?username=u&password=secret")
		if !errors.Is(err, vpn.ErrNotConnected) {
			t.Fatalf("expected ErrNotConnected, got %v", err)
		}
	}
	if hits.Load() != 0 || d.dials.Load() != 0 {
		t.Fatalf("traffic left without VPN: hits=%d dials=%d", hits.Load(), d.dials.Load())
	}
}

func TestEnvironmentProxyIgnored(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	tr := NewHTTPClient(&downDialer{}, time.Second).Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("transport must not use environment proxies")
	}
}

func TestRedactHidesCredentials(t *testing.T) {
	client := NewHTTPClient(nil, time.Second)
	_, err := client.Get("http://provider.example/live/user/secret/1.ts?password=secret")
	err = Redact(err)
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "user") {
		t.Fatalf("credentials visible in error: %v", err)
	}
	if !errors.Is(err, vpn.ErrNotConnected) {
		t.Fatalf("cause lost: %v", err)
	}
}
