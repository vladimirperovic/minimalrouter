package api

import (
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

const redactedSecret = "[REDACTED]"

// redactConfig returns a detached public view. The deep copy guarantees the
// redaction can never mutate canonical engine state, including preshared keys
// inside the peer slice.
//
// Only secrets are replaced. Identifiers the authenticated administrator
// manages on this page stay visible: WireGuard public keys (the dashboard shows
// and compares them, and they grant nothing without the device's private key)
// and the PPPoE username (edited in the WAN form). Diagnostic exports, which
// are meant to leave the appliance, remove those as well; see
// telemetry.RedactedSystemConfig.
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
		peer := &candidate.WireGuard.Peers[i]
		if peer.PresharedKey != redactedSecret {
			continue
		}
		// A preshared key belongs to one key pair. Restore it only for the
		// single stored peer with the same identifier and public key; an empty,
		// duplicated or re-keyed identity keeps the placeholder, which
		// validation rejects instead of silently pairing one client's secret
		// with another client's key.
		if existing, ok := uniqueWireGuardPeer(current.WireGuard.Peers, peer.ID, peer.PublicKey); ok {
			peer.PresharedKey = existing.PresharedKey
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

// uniqueWireGuardPeer returns the only peer carrying id (and, when publicKey
// is non-empty, that public key). Peer identifiers are not a validated
// uniqueness constraint, so a lookup that matches zero or several peers is
// reported as not found rather than resolved to the first match.
func uniqueWireGuardPeer(peers []config.WireGuardPeer, id, publicKey string) (config.WireGuardPeer, bool) {
	if id == "" {
		return config.WireGuardPeer{}, false
	}
	var found config.WireGuardPeer
	matches := 0
	for _, peer := range peers {
		if peer.ID != id || (publicKey != "" && peer.PublicKey != publicKey) {
			continue
		}
		found = peer
		matches++
	}
	return found, matches == 1
}
