package vpn

import (
	"fmt"
	"strings"
	"testing"
)

// wg-quick allows Address and AllowedIPs to be given on several lines;
// every line adds to the list.
func TestVPNRepeatedKeysAccumulate(t *testing.T) {
	priv, pub := testKeyPair(t)
	conf := fmt.Sprintf(`
[Interface]
PrivateKey = %s
Address = 192.0.2.2/32
Address = 2001:db8::2/128
DNS = 192.0.2.1

[Peer]
PublicKey = %s
AllowedIPs = 0.0.0.0/0
AllowedIPs = ::/0
Endpoint = vpn.example.com:51820
`, b64(priv), b64(pub))
	cfg, err := ParseConfig(strings.NewReader(conf))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(cfg.Addresses); got != "[192.0.2.2/32 2001:db8::2/128]" {
		t.Errorf("Addresses = %s, want both lines", got)
	}
	if got := fmt.Sprint(cfg.AllowedIPs); got != "[0.0.0.0/0 ::/0]" {
		t.Errorf("AllowedIPs = %s, want both lines", got)
	}
}
