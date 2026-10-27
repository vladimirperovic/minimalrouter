//! The public view of configuration: secrets replaced by a placeholder, and
//! unchanged placeholders resolved back before preview or apply.

use crate::apply::Transaction;
use crate::config::{SystemConfig, WireGuardPeer};

const REDACTED_SECRET: &str = "[REDACTED]";

/// A detached public view. Identifiers the administrator manages on this
/// page (WireGuard public keys, the PPPoE username) stay visible; diagnostic
/// exports remove those as well.
pub(crate) fn redact_config(cfg: &SystemConfig) -> SystemConfig {
    let mut public = cfg.deep_copy();
    public.wan.password = REDACTED_SECRET.into();
    public.wireguard.private_key = REDACTED_SECRET.into();
    public.wg_client.private_key = REDACTED_SECRET.into();
    if !public.wg_client.preshared_key.is_empty() {
        public.wg_client.preshared_key = REDACTED_SECRET.into();
    }
    for peer in public.wireguard.peers.iter_mut() {
        if !peer.preshared_key.is_empty() {
            peer.preshared_key = REDACTED_SECRET.into();
        }
    }
    public.cloudflare.api_token = REDACTED_SECRET.into();
    public.cloudflare.tunnel_token = REDACTED_SECRET.into();
    public.squid_proxy.password = REDACTED_SECRET.into();
    public.wifi.passphrase = REDACTED_SECRET.into();
    public
}

pub(crate) fn redact_transaction(tx: Option<&Transaction>) -> Option<Transaction> {
    tx.map(|tx| {
        let mut public = tx.clone();
        public.config = redact_config(&tx.config);
        public
    })
}

/// Never mutates the public candidate or canonical state.
pub(crate) fn restore_redacted_config(candidate: &SystemConfig, current: &SystemConfig) -> SystemConfig {
    let mut out = candidate.deep_copy();
    let restore = |value: &mut String, original: &str| {
        if value == REDACTED_SECRET {
            *value = original.to_string();
        }
    };
    restore(&mut out.wan.password, &current.wan.password);
    restore(&mut out.wireguard.private_key, &current.wireguard.private_key);
    restore(&mut out.wg_client.private_key, &current.wg_client.private_key);
    restore(&mut out.wg_client.preshared_key, &current.wg_client.preshared_key);
    for peer in out.wireguard.peers.iter_mut() {
        if peer.preshared_key != REDACTED_SECRET {
            continue;
        }
        // A preshared key belongs to one key pair: restore it only for the
        // single stored peer with the same identifier and public key.
        if let Some(existing) = unique_wireguard_peer(&current.wireguard.peers, &peer.id, &peer.public_key) {
            peer.preshared_key = existing.preshared_key.clone();
        }
    }
    restore(&mut out.cloudflare.api_token, &current.cloudflare.api_token);
    restore(&mut out.cloudflare.tunnel_token, &current.cloudflare.tunnel_token);
    restore(&mut out.squid_proxy.password, &current.squid_proxy.password);
    restore(&mut out.wifi.passphrase, &current.wifi.passphrase);
    out
}

/// The only peer with `id` (and `public_key` when given); zero or several
/// matches are "not found" rather than the first match.
pub(crate) fn unique_wireguard_peer<'a>(peers: &'a [WireGuardPeer], id: &str, public_key: &str) -> Option<&'a WireGuardPeer> {
    if id.is_empty() {
        return None;
    }
    let mut matches = peers.iter().filter(|p| p.id == id && (public_key.is_empty() || p.public_key == public_key));
    let found = matches.next()?;
    matches.next().is_none().then_some(found)
}
