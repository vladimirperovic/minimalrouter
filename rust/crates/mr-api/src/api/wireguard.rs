//! WireGuard peer provisioning: client keys are generated on the router,
//! only the public key and a preshared key are persisted, and the private
//! client configuration is returned exactly once in a no-store response.

use regex::Regex;
use std::sync::LazyLock;

use super::*;
use crate::config::WireGuardPeer;
use crate::services;

static ENDPOINT_HOST: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$").unwrap());

fn validate_wireguard_endpoint(value: &str) -> std::result::Result<(), String> {
    if value.is_empty() || value.len() > 255 || value.contains(['\r', '\n', '\t', '\0']) {
        return Err("server_endpoint must be a host and UDP port".into());
    }
    let (host, port) = net::split_host_port(value).ok().filter(|(h, _)| !h.is_empty()).ok_or("server_endpoint must use host:port format")?;
    match net::parse_ip(&host) {
        Some(ip) if ip.to4().is_none() => return Err("IPv6 endpoints are unavailable while IPv6 is disabled".into()),
        Some(_) => {}
        None if !ENDPOINT_HOST.is_match(&host) || host.contains("..") => return Err("server_endpoint contains an invalid hostname".into()),
        None => {}
    }
    match config::go_atoi(&port) {
        Some(p) if (1024..=65535).contains(&p) => Ok(()),
        _ => Err("server_endpoint port must be between 1024 and 65535".into()),
    }
}

fn resolve_wireguard_server_endpoint(candidate: &SystemConfig, requested: &str) -> std::result::Result<String, String> {
    let mut endpoint = requested.trim().to_string();
    if endpoint.is_empty() {
        let domain = candidate.cloudflare.domain.trim();
        if !domain.is_empty() {
            endpoint = net::join_host_port(domain, &candidate.wireguard.listen_port.to_string());
        }
    }
    if endpoint.is_empty() {
        return Err("configure Dynamic DNS or provide a server_endpoint".into());
    }
    validate_wireguard_endpoint(&endpoint)?;
    Ok(endpoint)
}

fn ipv4_u32(ip: &net::IP) -> Option<u32> {
    ip.to4().and_then(|v4| v4.bytes().try_into().ok()).map(u32::from_be_bytes)
}

fn u32_ipv4(value: u32) -> String {
    std::net::Ipv4Addr::from(value).to_string()
}

/// The first free usable address after the server, wrapping once. Disabled
/// peers keep their addresses so re-enabling cannot duplicate a route.
fn next_free_wireguard_ip(server_ip: &net::IP, subnet: &str, peers: &[WireGuardPeer]) -> std::result::Result<String, String> {
    let used: std::collections::HashSet<u32> = peers
        .iter()
        .flat_map(|p| p.allowed_ips.iter())
        .filter_map(|allowed| net::parse_cidr(allowed.trim()))
        .filter(|(ip, n)| n.mask.size() == (32, 32) && ip.to4().is_some())
        .filter_map(|(ip, _)| ipv4_u32(&ip))
        .collect();
    let (_, network) = net::parse_cidr(subnet).ok_or_else(|| format!("invalid WireGuard subnet {}", subnet))?;
    let (Some(net_value), Some(server_value)) = (ipv4_u32(&network.ip), ipv4_u32(server_ip)) else {
        return Err("WireGuard subnet is not a valid IPv4 network for the server address".into());
    };
    if !network.contains(server_ip) {
        return Err("WireGuard subnet is not a valid IPv4 network for the server address".into());
    }
    let (ones, bits) = network.mask.size();
    if bits != 32 || ones > 30 {
        return Err(format!("no free address available in WireGuard subnet {}", subnet));
    }
    let broadcast = net_value | ((1u64 << (32 - ones)) - 1) as u32;
    let (first, last) = (net_value + 1, broadcast - 1);
    let find = |start: u32, end: u32| (start..=end).find(|c| *c != server_value && !used.contains(c));
    if server_value < last {
        if let Some(found) = find(server_value + 1, last) {
            return Ok(u32_ipv4(found));
        }
    }
    if server_value > first {
        if let Some(found) = find(first, server_value - 1) {
            return Ok(u32_ipv4(found));
        }
    }
    Err(format!("no free address available in WireGuard subnet {}", subnet))
}

/// Exactly one peer per identifier: a restored configuration may repeat one,
/// and acting on the first match would change a different device.
fn find_wireguard_peer(peers: &[WireGuardPeer], id: &str) -> std::result::Result<usize, Response> {
    let mut index = None;
    for (i, peer) in peers.iter().enumerate() {
        if id.is_empty() || peer.id != id {
            continue;
        }
        if index.is_some() {
            return Err(Response::error("Several WireGuard peers share this identifier; correct the configuration before changing them", 409));
        }
        index = Some(i);
    }
    index.ok_or_else(|| Response::error("WireGuard peer not found", 404))
}

fn client_bundle(
    candidate: &SystemConfig,
    client_private: &str,
    client_cidr: &str,
    server_public: &str,
    endpoint: &str,
    psk: &str,
) -> Result<services::ClientConfigBundle> {
    services::generate_client_config(
        client_private,
        client_cidr,
        server_public,
        endpoint,
        psk,
        &candidate.dhcp.dns_servers.join(", "),
        &[candidate.wireguard.address.clone(), candidate.lan.cidr.clone()],
    )
}

impl Server {
    fn apply_peer_change(&self, r: &Request<'_>, prefix: &str, candidate: SystemConfig, body: impl FnOnce(&Transaction) -> Value) -> Response {
        let (tx, result) = self.engine.process_transaction(&format!("{}-{}", prefix, Time::now().unix_nano()), candidate);
        self.audit_config_request(r, Some(&tx));
        if let Err(err) = result {
            return Response::json(422, &json!({"error": err.to_string(), "tx": redact_transaction(Some(&tx))}));
        }
        let status = if tx.current_state == apply::State::AwaitingConfirmation { 202 } else { 200 };
        write_json(status, &body(&tx))
    }

    pub(crate) fn handle_provision_wireguard_peer(&self, r: &mut Request<'_>) -> Response {
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            name: String,
            client_ip_address: String,
            server_endpoint: String,
        }
        let Ok(mut req) = decode_json::<Req>(r) else { return Response::error("Invalid JSON payload", 400) };
        req.name = req.name.trim().to_string();
        req.client_ip_address = req.client_ip_address.strip_suffix("/32").unwrap_or(&req.client_ip_address).trim().to_string();
        if req.name.is_empty() {
            return Response::error("name is required", 422);
        }
        let mut candidate = self.engine.get_current_config();
        if !candidate.wan.enabled {
            return Response::error("Configure and verify the WAN connection before enabling WireGuard", 409);
        }
        let Some((server_ip, network)) = net::parse_cidr(&candidate.wireguard.address) else {
            return Response::error("WireGuard subnet is invalid", 422);
        };
        if req.client_ip_address.is_empty() {
            match next_free_wireguard_ip(&server_ip, &candidate.wireguard.address, &candidate.wireguard.peers) {
                Ok(ip) => req.client_ip_address = ip,
                Err(err) => return Response::error(&err, 409),
            }
        }
        let endpoint = match resolve_wireguard_server_endpoint(&candidate, &req.server_endpoint) {
            Ok(endpoint) => endpoint,
            Err(err) => return Response::error(&err, 422),
        };
        let client_ip =
            net::parse_ip(&req.client_ip_address).filter(|ip| ip.to4().is_some() && network.contains(ip) && *ip != server_ip && ip.to4() != server_ip.to4());
        let Some(client_ip) = client_ip else {
            return Response::error("client_ip_address must be a unique IPv4 address inside the WireGuard subnet", 422);
        };
        let client_cidr = format!("{}/32", client_ip);
        if candidate.wireguard.peers.iter().any(|p| p.allowed_ips.iter().any(|a| *a == client_cidr)) {
            return Response::error("client_ip_address is already reserved by another WireGuard peer", 409);
        }
        let Ok((client_private, client_public)) = services::generate_wireguard_keypair() else {
            return Response::error("Could not generate WireGuard client key", 500);
        };
        let Ok(psk) = services::generate_wireguard_preshared_key() else { return Response::error("Could not generate WireGuard preshared key", 500) };
        if candidate.wireguard.private_key.is_empty() {
            match services::generate_wireguard_keypair() {
                Ok((server_private, _)) => candidate.wireguard.private_key = server_private,
                Err(_) => return Response::error("Could not generate WireGuard server key", 500),
            }
        }
        let Ok(server_public) = services::wireguard_public_key(&candidate.wireguard.private_key) else {
            return Response::error("Stored WireGuard server key is invalid", 500);
        };
        let mut id_bytes = [0u8; 18];
        if crate::goish::rand::fill(&mut id_bytes).is_err() {
            return Response::error("Could not generate peer identifier", 500);
        }
        let peer_id = format!("wg-{}", base64::Engine::encode(&base64::engine::general_purpose::URL_SAFE_NO_PAD, id_bytes));
        candidate.wireguard.enabled = true;
        candidate.wireguard.peers.push(WireGuardPeer {
            id: peer_id.clone(),
            name: req.name.clone(),
            public_key: client_public.clone(),
            preshared_key: psk.clone(),
            allowed_ips: vec![client_cidr.clone()].into(),
            enabled: true,
            ..Default::default()
        });
        if let Err(err) = candidate.validate() {
            return Response::error(&err.to_string(), 422);
        }
        // All one-time client material exists before router state changes.
        let Ok(bundle) = client_bundle(&candidate, &client_private, &client_cidr, &server_public, &endpoint, &psk) else {
            return Response::error("Could not generate WireGuard client configuration", 500);
        };
        self.apply_peer_change(r, "wireguard-peer", candidate, |tx| {
            json!({
                "peer": {"id": peer_id, "name": req.name, "client_ip": client_cidr, "public_key": client_public},
                "client_config": bundle.config_text, "qr_code_data": bundle.qr_code_data, "tx": redact_transaction(Some(tx)),
            })
        })
    }

    /// Replaces a peer's client key material; the old configuration stops
    /// working once the transaction applies.
    pub(crate) fn handle_reissue_wireguard_peer_configuration(&self, r: &mut Request<'_>) -> Response {
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            server_endpoint: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return Response::error("Invalid JSON payload", 400) };
        let mut candidate = self.engine.get_current_config();
        let index = match find_wireguard_peer(&candidate.wireguard.peers, &r.path_value("id")) {
            Ok(index) => index,
            Err(response) => return response,
        };
        let Some((server_ip, network)) = net::parse_cidr(&candidate.wireguard.address) else {
            return Response::error("WireGuard subnet is invalid", 422);
        };
        let peer = candidate.wireguard.peers[index].clone();
        if peer.allowed_ips.len() != 1 {
            return Response::error("WireGuard peer must have exactly one client IPv4 address", 422);
        }
        let client = net::parse_cidr(peer.allowed_ips[0].trim())
            .filter(|(ip, n)| ip.to4().is_some() && n.mask.size() == (32, 32) && network.contains(ip) && ip.to4() != server_ip.to4());
        let Some((client_ip, _)) = client else { return Response::error("WireGuard peer client address is invalid", 422) };
        let client_cidr = format!("{}/32", client_ip);
        let endpoint = match resolve_wireguard_server_endpoint(&candidate, &req.server_endpoint) {
            Ok(endpoint) => endpoint,
            Err(err) => return Response::error(&err, 422),
        };
        let Ok((client_private, client_public)) = services::generate_wireguard_keypair() else {
            return Response::error("Could not generate WireGuard client key", 500);
        };
        let Ok(psk) = services::generate_wireguard_preshared_key() else { return Response::error("Could not generate WireGuard preshared key", 500) };
        let Ok(server_public) = services::wireguard_public_key(&candidate.wireguard.private_key) else {
            return Response::error("Stored WireGuard server key is invalid", 500);
        };
        candidate.wireguard.peers[index].public_key = client_public.clone();
        candidate.wireguard.peers[index].preshared_key = psk.clone();
        if let Err(err) = candidate.validate() {
            return Response::error(&err.to_string(), 422);
        }
        let Ok(bundle) = client_bundle(&candidate, &client_private, &client_cidr, &server_public, &endpoint, &psk) else {
            return Response::error("Could not generate WireGuard client configuration", 500);
        };
        self.apply_peer_change(r, "wireguard-peer-reissue", candidate, |tx| {
            json!({
                "peer": {"id": peer.id, "name": peer.name, "client_ip": client_cidr, "public_key": client_public},
                "client_config": bundle.config_text, "qr_code_data": bundle.qr_code_data, "tx": redact_transaction(Some(tx)),
            })
        })
    }

    pub(crate) fn handle_delete_wireguard_peer(&self, r: &mut Request<'_>) -> Response {
        let mut candidate = self.engine.get_current_config();
        let index = match find_wireguard_peer(&candidate.wireguard.peers, &r.path_value("id")) {
            Ok(index) => index,
            Err(response) => return response,
        };
        let removed = candidate.wireguard.peers.remove(index);
        if let Err(err) = candidate.validate() {
            return Response::json(422, &json!({"error": err.to_string()}));
        }
        self.apply_peer_change(
            r,
            "wireguard-peer-delete",
            candidate,
            |tx| json!({"peer": {"id": removed.id, "name": removed.name}, "tx": redact_transaction(Some(tx))}),
        )
    }

    /// A fresh key pair for the outbound tunnel, stored only when saved.
    pub(crate) fn handle_wireguard_client_keys(&self, _r: &mut Request<'_>) -> Response {
        match services::generate_wireguard_keypair() {
            Ok((private_key, public_key)) => write_json(200, &json!({"private_key": private_key, "public_key": public_key})),
            Err(_) => Response::error("could not generate key pair", 500),
        }
    }

    /// The exact client IP and endpoint the next peer will receive, so the
    /// frontend never duplicates allocation logic.
    pub(crate) fn handle_wireguard_provisioning_preview(&self, _r: &mut Request<'_>) -> Response {
        let candidate = self.engine.get_current_config();
        let domain = candidate.cloudflare.domain.trim();
        let endpoint = if domain.is_empty() { String::new() } else { net::join_host_port(domain, &candidate.wireguard.listen_port.to_string()) };
        let mut preview = json!({
            "client_ip": "", "server_endpoint": endpoint, "ddns_configured": !endpoint.is_empty(),
            "wireguard_enabled": candidate.wireguard.enabled,
            // Presence from canonical state, never the redacted view.
            "server_key_configured": !candidate.wireguard.private_key.is_empty(),
        });
        let Some((server_ip, _)) = net::parse_cidr(&candidate.wireguard.address) else {
            preview["error"] = json!("WireGuard subnet is invalid");
            return write_json(422, &preview);
        };
        match next_free_wireguard_ip(&server_ip, &candidate.wireguard.address, &candidate.wireguard.peers) {
            Ok(ip) => {
                preview["client_ip"] = json!(ip);
                write_json(200, &preview)
            }
            Err(err) => {
                preview["error"] = json!(err);
                write_json(409, &preview)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn allocation_skips_server_and_reserved_addresses() {
        let server = net::parse_ip("10.6.0.1").unwrap();
        let peers = vec![WireGuardPeer { allowed_ips: vec!["10.6.0.2/32".to_string()].into(), ..Default::default() }];
        assert_eq!(next_free_wireguard_ip(&server, "10.6.0.1/24", &peers).unwrap(), "10.6.0.3");
        let server = net::parse_ip("10.6.0.254").unwrap();
        assert_eq!(next_free_wireguard_ip(&server, "10.6.0.254/24", &[]).unwrap(), "10.6.0.1");
        assert!(next_free_wireguard_ip(&server, "10.6.0.254/31", &[]).is_err());
        assert_eq!(validate_wireguard_endpoint("vpn.example.com:51820"), Ok(()));
        assert_eq!(validate_wireguard_endpoint("[2001:db8::1]:51820").unwrap_err(), "IPv6 endpoints are unavailable while IPv6 is disabled");
        assert_eq!(validate_wireguard_endpoint("vpn..example.com:51820").unwrap_err(), "server_endpoint contains an invalid hostname");
        assert_eq!(validate_wireguard_endpoint("vpn.example.com:53").unwrap_err(), "server_endpoint port must be between 1024 and 65535");
    }
}
