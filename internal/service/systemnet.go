package service

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"github.com/silirim/vodmax/internal/vpn"
)

// systemNetwork is the "VPN" of kind system: the user runs a VPN outside
// VodMax (on the device, or on the router for the whole network), so the
// provider is reached through the normal network. VodMax cannot verify
// that this traffic really goes through a VPN.
type systemNetwork struct{}

func (systemNetwork) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d := net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return d.DialContext(ctx, network, address)
}

func (systemNetwork) Up() bool { return true }

// Status reports it as connected: there is no tunnel to watch.
func (systemNetwork) Status() vpn.Status { return vpn.Status{Connected: true} }

func (systemNetwork) Close() {}

// imageNetwork is the normal network for images. Their addresses come
// from playlists, so they must not reach this machine's own services
// (loopback) or link-local ones (cloud metadata), not even through a
// redirect. LAN addresses stay allowed: a playlist server in the house
// may host its logos.
type imageNetwork struct{ systemNetwork }

func (imageNetwork) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d := net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: refuseLocal}
	return d.DialContext(ctx, network, address)
}

var errLocalAddress = errors.New("indirizzo locale non permesso per le immagini")

// refuseLocal checks the address actually dialled, after name resolution.
func refuseLocal(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return errLocalAddress
	}
	return nil
}
