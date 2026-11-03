package service

import (
	"context"
	"errors"
)

// ProviderNetwork is how VodMax reaches the providers (decision 0039).
// VPNOnly, the default: a subscription goes through its VPN and is never
// contacted without one. Off, a subscription may have no VPN and use the
// normal network of the server, as a VPN "outside VodMax" does.
type ProviderNetwork struct {
	VPNOnly bool `json:"vpn_only"`
}

const providerNetworkKey = "provider_network"

var errVPNOnly = errors.New("l'abbonamento non ha una VPN e i provider si contattano solo tramite VPN: scegline una nell'abbonamento oppure cambia l'impostazione in Impostazioni → VPN")

func (s *Service) ProviderNetwork(ctx context.Context) (ProviderNetwork, error) {
	n := ProviderNetwork{VPNOnly: true}
	err := s.st.Setting(ctx, providerNetworkKey, &n)
	return n, err
}

func (s *Service) SetProviderNetwork(ctx context.Context, n ProviderNetwork) error {
	return s.st.SetSetting(ctx, providerNetworkKey, n)
}

// checkSubscriptionVPN: a VPN that exists, or none when the providers may
// be reached without one.
func (s *Service) checkSubscriptionVPN(ctx context.Context, id int64) error {
	if id == 0 {
		n, err := s.ProviderNetwork(ctx)
		if err != nil {
			return err
		}
		if n.VPNOnly {
			return invalid("scegli una VPN: i provider si contattano solo tramite VPN (Impostazioni → VPN)")
		}
		return nil
	}
	if _, err := s.st.VPN(ctx, id); err != nil {
		return invalid("la VPN scelta non esiste")
	}
	return nil
}
