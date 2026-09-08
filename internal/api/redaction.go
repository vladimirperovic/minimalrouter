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

// restoreRedactedConfig resolves unchanged secret placeholders before either
// preview or apply. It never mutates the public candidate or canonical state.
func restoreRedactedConfig(candidate, current config.SystemConfig) config.SystemConfig {
	candidate = candidate.DeepCopy()
	if candidate.WAN.Password == redactedSecret {
		candidate.WAN.Password = current.WAN.Password
	}
	if candidate.WireGuard.PrivateKey == redactedSecret {
		candidate.WireGuard.PrivateKey = current.WireGuard.PrivateKey
	}
	if candidate.WGClient.PrivateKey == redactedSecret {
		candidate.WGClient.PrivateKey = current.WGClient.PrivateKey
	}
	if candidate.WGClient.PresharedKey == redactedSecret {
		candidate.WGClient.PresharedKey = current.WGClient.PresharedKey
	}
	for i := range candidate.WireGuard.Peers {
		if candidate.WireGuard.Peers[i].PresharedKey != redactedSecret {
			continue
		}
		for _, existing := range current.WireGuard.Peers {
			if existing.ID == candidate.WireGuard.Peers[i].ID {
				candidate.WireGuard.Peers[i].PresharedKey = existing.PresharedKey
				break
			}
		}
	}
	if candidate.Cloudflare.APIToken == redactedSecret {
		candidate.Cloudflare.APIToken = current.Cloudflare.APIToken
	}
	if candidate.Cloudflare.TunnelToken == redactedSecret {
		candidate.Cloudflare.TunnelToken = current.Cloudflare.TunnelToken
	}
	if candidate.SquidProxy.Password == redactedSecret {
		candidate.SquidProxy.Password = current.SquidProxy.Password
	}
	if candidate.WiFi.Passphrase == redactedSecret {
		candidate.WiFi.Passphrase = current.WiFi.Passphrase
	}

	return candidate
}
