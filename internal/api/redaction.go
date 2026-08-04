package api

import (
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

const redactedSecret = "[REDACTED]"

// redactConfig returns a detached public view. The deep copy guarantees the
// redaction can never mutate canonical engine state, including preshared keys
// inside the peer slice.
func redactConfig(cfg config.SystemConfig) config.SystemConfig {
	public := cfg.DeepCopy()

	public.WAN.Password = redactedSecret
	public.WireGuard.PrivateKey = redactedSecret
	public.WGClient.PrivateKey = redactedSecret
	if public.WGClient.PresharedKey != "" {
		public.WGClient.PresharedKey = redactedSecret
	}
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
