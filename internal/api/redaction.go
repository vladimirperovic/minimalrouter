package api

import (
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

const redactedSecret = "[REDACTED]"

// redactConfig returns a detached public view. In particular, the peer slice
// must not share backing storage with canonical state before preshared keys are
// overwritten.
func redactConfig(cfg config.SystemConfig) config.SystemConfig {
	public := cfg
	public.WireGuard.Peers = append([]config.WireGuardPeer(nil), cfg.WireGuard.Peers...)

	public.WAN.Password = redactedSecret
	public.WireGuard.PrivateKey = redactedSecret
	for i := range public.WireGuard.Peers {
		if public.WireGuard.Peers[i].PresharedKey != "" {
			public.WireGuard.Peers[i].PresharedKey = redactedSecret
		}
	}
	public.Cloudflare.APIToken = redactedSecret
	public.Cloudflare.TunnelToken = redactedSecret
	public.SquidProxy.Password = redactedSecret
	public.WiFi.Passphrase = redactedSecret
	return public
}

func redactTransaction(tx *apply.Transaction) *apply.Transaction {
	if tx == nil {
		return nil
	}
	public := *tx
	public.Config = redactConfig(tx.Config)
	return &public
}
