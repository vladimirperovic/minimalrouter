use crate::goish::net;

/// Makes equivalent IPv4 spellings comparable without changing configuration
/// or accepting invalid input. Validation remains the caller's job.
pub fn ipv4_identity(value: &str) -> String {
    match net::parse_ipv4(value) {
        Some(ip) => ip.to_string(),
        None => value.to_string(),
    }
}

/// Compares the colon, dash and dotted forms accepted by `net.ParseMAC`.
pub fn mac_identity(value: &str) -> String {
    match net::parse_mac(value) {
        Some(mac) if mac.len() == 6 => net::mac_string(&mac),
        _ => value.to_string(),
    }
}
