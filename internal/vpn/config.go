// Package vpn implements VodMax's internal VPN: a userspace WireGuard tunnel
// with its own TCP/IP stack. Provider traffic can only leave through it.
package vpn

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

// Key is a WireGuard Curve25519 key.
type Key [32]byte

// ParseKey decodes a base64 key as written in wg-quick files.
func ParseKey(s string) (Key, error) {
	var k Key
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != len(k) {
		return k, errors.New("chiave WireGuard non valida")
	}
	copy(k[:], raw)
	return k, nil
}

func (k Key) hex() string { return hex.EncodeToString(k[:]) }

// String never reveals the key: configs end up in logs and error messages.
func (k Key) String() string { return "(redatto)" }

// Config is a single-peer WireGuard configuration, the shape consumer VPN
// providers (Proton, Mullvad, NordVPN, ...) hand out.
type Config struct {
	PrivateKey          Key
	Addresses           []netip.Prefix
	DNS                 []netip.Addr
	MTU                 int
	PeerPublicKey       Key
	PresharedKey        *Key
	AllowedIPs          []netip.Prefix
	Endpoint            string // host:port, resolved on the normal network
	PersistentKeepalive int
	// Ignored lists wg-quick keys that VodMax accepts but never applies,
	// such as PostUp scripts or routing tables: the tunnel is internal.
	Ignored []string
}

const (
	defaultMTU       = 1420
	defaultKeepalive = 25
)

// ignoredKeys belong to wg-quick's system integration and are safe to skip.
var ignoredKeys = map[string]bool{
	"table": true, "preup": true, "postup": true, "predown": true, "postdown": true,
	"saveconfig": true, "listenport": true, "fwmark": true,
}

// ParseConfig reads a wg-quick style .conf file.
func ParseConfig(r io.Reader) (*Config, error) {
	cfg := &Config{}
	section, peers := "", 0
	scanner := bufio.NewScanner(io.LimitReader(r, 64*1024))
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if i := strings.IndexAny(text, "#;"); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			section = strings.ToLower(strings.TrimSpace(text[1 : len(text)-1]))
			switch section {
			case "interface":
			case "peer":
				peers++
				if peers > 1 {
					return nil, errors.New("è supportato un solo [Peer]")
				}
			default:
				return nil, fmt.Errorf("riga %d: sezione sconosciuta %q", line, section)
			}
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("riga %d: atteso chiave = valore", line)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if err := cfg.set(section, key, value); err != nil {
			return nil, fmt.Errorf("riga %d (%s): %w", line, key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return cfg, cfg.Validate()
}

func (c *Config) set(section, key, value string) error {
	var err error
	switch section + "." + key {
	case "interface.privatekey":
		c.PrivateKey, err = ParseKey(value)
	case "interface.address": // may be repeated: each line adds
		var p []netip.Prefix
		p, err = parsePrefixes(value, true)
		c.Addresses = append(c.Addresses, p...)
	case "interface.dns":
		for _, part := range splitList(value) {
			addr, perr := netip.ParseAddr(part)
			if perr != nil {
				// Search domains are a system resolver feature; skip them.
				c.Ignored = append(c.Ignored, "DNS "+part)
				continue
			}
			c.DNS = append(c.DNS, addr)
		}
	case "interface.mtu":
		c.MTU, err = strconv.Atoi(value)
		if err == nil && (c.MTU < 1280 || c.MTU > 9000) {
			err = errors.New("MTU fuori intervallo")
		}
	case "peer.publickey":
		c.PeerPublicKey, err = ParseKey(value)
	case "peer.presharedkey":
		var k Key
		k, err = ParseKey(value)
		c.PresharedKey = &k
	case "peer.allowedips":
		var p []netip.Prefix
		p, err = parsePrefixes(value, false)
		c.AllowedIPs = append(c.AllowedIPs, p...)
	case "peer.endpoint":
		c.Endpoint = value
	case "peer.persistentkeepalive":
		c.PersistentKeepalive, err = strconv.Atoi(value)
		if err == nil && (c.PersistentKeepalive < 0 || c.PersistentKeepalive > 65535) {
			err = errors.New("valore fuori intervallo")
		}
	default:
		if section == "" {
			return errors.New("chiave fuori da una sezione")
		}
		if !ignoredKeys[key] {
			return errors.New("chiave sconosciuta")
		}
		c.Ignored = append(c.Ignored, key)
	}
	return err
}

// Validate checks that the configuration can bring a tunnel up.
func (c *Config) Validate() error {
	var zero Key
	switch {
	case c.PrivateKey == zero:
		return errors.New("manca PrivateKey")
	case len(c.Addresses) == 0:
		return errors.New("manca Address")
	case c.PeerPublicKey == zero:
		return errors.New("manca [Peer] PublicKey")
	case c.Endpoint == "":
		return errors.New("manca Endpoint")
	case len(c.AllowedIPs) == 0:
		return errors.New("manca AllowedIPs")
	case len(c.DNS) == 0:
		// Without a DNS server inside the tunnel, provider names would have
		// to be resolved on the normal network: refuse instead of leaking.
		return errors.New("manca DNS: serve un server DNS raggiungibile nel tunnel")
	}
	if _, _, err := splitHostPort(c.Endpoint); err != nil {
		return fmt.Errorf("Endpoint non valido: %w", err)
	}
	return nil
}

func (c *Config) mtu() int {
	if c.MTU == 0 {
		return defaultMTU
	}
	return c.MTU
}

func (c *Config) keepalive() int {
	if c.PersistentKeepalive == 0 {
		// A keepalive makes the handshake happen as soon as the tunnel is
		// up, so its status is known before the first provider request.
		return defaultKeepalive
	}
	return c.PersistentKeepalive
}

// uapi renders the configuration for wireguard-go's IpcSet, with the
// endpoint already resolved to an address.
func (c *Config) uapi(endpoint netip.AddrPort) string {
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", c.PrivateKey.hex())
	fmt.Fprintf(&b, "replace_peers=true\npublic_key=%s\n", c.PeerPublicKey.hex())
	if c.PresharedKey != nil {
		fmt.Fprintf(&b, "preshared_key=%s\n", c.PresharedKey.hex())
	}
	fmt.Fprintf(&b, "endpoint=%s\n", endpoint)
	fmt.Fprintf(&b, "persistent_keepalive_interval=%d\nreplace_allowed_ips=true\n", c.keepalive())
	for _, p := range c.AllowedIPs {
		fmt.Fprintf(&b, "allowed_ip=%s\n", p)
	}
	return b.String()
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parsePrefixes(value string, hostOnly bool) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range splitList(value) {
		p, err := netip.ParsePrefix(part)
		if err != nil {
			addr, aerr := netip.ParseAddr(part)
			if aerr != nil {
				return nil, fmt.Errorf("indirizzo non valido %q", part)
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		if !hostOnly {
			p = p.Masked()
		}
		out = append(out, p)
	}
	return out, nil
}

func splitHostPort(endpoint string) (string, int, error) {
	i := strings.LastIndex(endpoint, ":")
	if i <= 0 {
		return "", 0, errors.New("atteso host:porta")
	}
	host := strings.Trim(endpoint[:i], "[]")
	port, err := strconv.Atoi(endpoint[i+1:])
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("porta non valida")
	}
	return host, port, nil
}
