//! Go `net` package semantics that validation, generators and the API rely on.
//!
//! The Rust standard library parses addresses differently from Go in small but
//! observable ways (IPv4-mapped IPv6 handling, CIDR prefixes with leading zeros,
//! MAC spellings, host:port splitting). Every check that decides whether a
//! configuration is accepted must agree with the Go implementation, so these
//! helpers reproduce Go's rules rather than approximating them.

use std::fmt;
use std::net::{Ipv4Addr, Ipv6Addr};

const V4_IN_V6_PREFIX: [u8; 12] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff];

/// A Go `net.IP`: 4 or 16 bytes. `ParseIP` always yields the 16-byte form.
#[derive(Clone, PartialEq, Eq, Hash)]
pub struct IP(Vec<u8>);

impl fmt::Debug for IP {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "IP({})", self)
    }
}

impl IP {
    pub fn from_bytes(bytes: &[u8]) -> IP {
        IP(bytes.to_vec())
    }

    pub fn v4(a: u8, b: u8, c: u8, d: u8) -> IP {
        let mut out = V4_IN_V6_PREFIX.to_vec();
        out.extend_from_slice(&[a, b, c, d]);
        IP(out)
    }

    pub fn bytes(&self) -> &[u8] {
        &self.0
    }

    pub fn len(&self) -> usize {
        self.0.len()
    }

    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }

    /// `ip.To4()`: the 4-byte form for IPv4 and IPv4-mapped IPv6, else None.
    pub fn to4(&self) -> Option<IP> {
        match self.0.len() {
            4 => Some(self.clone()),
            16 if self.0[..12] == V4_IN_V6_PREFIX => Some(IP(self.0[12..].to_vec())),
            _ => None,
        }
    }

    pub fn to4_array(&self) -> Option<[u8; 4]> {
        self.to4().map(|ip| [ip.0[0], ip.0[1], ip.0[2], ip.0[3]])
    }

    pub fn to16(&self) -> Option<IP> {
        match self.0.len() {
            4 => {
                let mut out = V4_IN_V6_PREFIX.to_vec();
                out.extend_from_slice(&self.0);
                Some(IP(out))
            }
            16 => Some(self.clone()),
            _ => None,
        }
    }

    /// `ip.Equal(x)`: a 4-byte and 16-byte form of the same IPv4 address are equal.
    pub fn equal(&self, other: &IP) -> bool {
        let (a, b) = (&self.0, &other.0);
        if a.len() == b.len() {
            return a == b;
        }
        if a.len() == 4 && b.len() == 16 {
            return b[..12] == V4_IN_V6_PREFIX && a[..] == b[12..];
        }
        if a.len() == 16 && b.len() == 4 {
            return a[..12] == V4_IN_V6_PREFIX && a[12..] == b[..];
        }
        false
    }

    pub fn equal_opt(&self, other: Option<&IP>) -> bool {
        other.is_some_and(|other| self.equal(other))
    }

    pub fn is_unspecified(&self) -> bool {
        self.equal(&IP::v4(0, 0, 0, 0)) || self.equal(&IP(vec![0; 16]))
    }

    pub fn is_loopback(&self) -> bool {
        if let Some(v4) = self.to4() {
            return v4.0[0] == 127;
        }
        let mut loopback = vec![0u8; 16];
        loopback[15] = 1;
        self.equal(&IP(loopback))
    }

    pub fn is_multicast(&self) -> bool {
        if let Some(v4) = self.to4() {
            return v4.0[0] & 0xf0 == 0xe0;
        }
        self.0.len() == 16 && self.0[0] == 0xff
    }

    pub fn is_private(&self) -> bool {
        if let Some(v4) = self.to4() {
            let b = &v4.0;
            return b[0] == 10 || (b[0] == 172 && b[1] & 0xf0 == 16) || (b[0] == 192 && b[1] == 168);
        }
        self.0.len() == 16 && self.0[0] & 0xfe == 0xfc
    }

    pub fn is_link_local_unicast(&self) -> bool {
        if let Some(v4) = self.to4() {
            return v4.0[0] == 169 && v4.0[1] == 254;
        }
        self.0.len() == 16 && self.0[0] == 0xfe && self.0[1] & 0xc0 == 0x80
    }

    pub fn is_global_unicast(&self) -> bool {
        let len = self.0.len();
        (len == 4 || len == 16)
            && !self.equal(&IP::v4(255, 255, 255, 255))
            && !self.is_unspecified()
            && !self.is_loopback()
            && !self.is_multicast()
            && !self.is_link_local_unicast()
    }

    /// `ip.Mask(mask)`.
    pub fn mask(&self, mask: &IPMask) -> Option<IP> {
        let mut ip = self.0.as_slice();
        let mut m = mask.0.as_slice();
        if m.len() == 16 && ip.len() == 4 && m[..12].iter().all(|b| *b == 0xff) {
            m = &m[12..];
        }
        if m.len() == 4 && ip.len() == 16 && ip[..12] == V4_IN_V6_PREFIX {
            ip = &ip[12..];
        }
        if ip.len() != m.len() {
            return None;
        }
        Some(IP(ip.iter().zip(m).map(|(a, b)| a & b).collect()))
    }

    pub fn to_ipv4addr(&self) -> Option<Ipv4Addr> {
        self.to4_array().map(Ipv4Addr::from)
    }
}

impl fmt::Display for IP {
    /// `ip.String()`.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.0.is_empty() {
            return f.write_str("<nil>");
        }
        if let Some(v4) = self.to4_array() {
            return write!(f, "{}", Ipv4Addr::from(v4));
        }
        if self.0.len() != 16 {
            f.write_str("?")?;
            for b in &self.0 {
                write!(f, "{:02x}", b)?;
            }
            return Ok(());
        }
        let mut octets = [0u8; 16];
        octets.copy_from_slice(&self.0);
        write!(f, "{}", Ipv6Addr::from(octets))
    }
}

/// `net.ParseIP`: dotted IPv4 (no leading zeros) or IPv6 without a zone.
pub fn parse_ip(value: &str) -> Option<IP> {
    if value.contains('%') {
        return None;
    }
    if let Ok(v4) = parse_ipv4_strict(value) {
        let o = v4.octets();
        return Some(IP::v4(o[0], o[1], o[2], o[3]));
    }
    if value.contains(':') {
        if let Ok(v6) = value.parse::<Ipv6Addr>() {
            return Some(IP(v6.octets().to_vec()));
        }
    }
    None
}

/// Go 1.17+ dotted-decimal: four fields, 1-3 digits, value <= 255, no leading zeros.
fn parse_ipv4_strict(value: &str) -> Result<Ipv4Addr, ()> {
    let mut out = [0u8; 4];
    let mut count = 0;
    for part in value.split('.') {
        if count == 4 || part.is_empty() || part.len() > 3 || !part.bytes().all(|b| b.is_ascii_digit()) {
            return Err(());
        }
        if part.len() > 1 && part.starts_with('0') {
            return Err(());
        }
        let n: u16 = part.parse().map_err(|_| ())?;
        if n > 255 {
            return Err(());
        }
        out[count] = n as u8;
        count += 1;
    }
    if count != 4 {
        return Err(());
    }
    Ok(Ipv4Addr::from(out))
}

/// The config package's `parseIPv4`: `net.ParseIP(value).To4()`.
pub fn parse_ipv4(value: &str) -> Option<IP> {
    parse_ip(value).and_then(|ip| ip.to4())
}

#[derive(Clone, PartialEq, Eq, Hash, Debug)]
pub struct IPMask(Vec<u8>);

impl IPMask {
    pub fn bytes(&self) -> &[u8] {
        &self.0
    }

    /// `net.CIDRMask(ones, bits)`.
    pub fn cidr(ones: usize, bits: usize) -> Option<IPMask> {
        if (bits != 32 && bits != 128) || ones > bits {
            return None;
        }
        let mut out = vec![0u8; bits / 8];
        let mut n = ones;
        for b in out.iter_mut() {
            if n >= 8 {
                *b = 0xff;
                n -= 8;
                continue;
            }
            *b = !(0xffu8.checked_shr(n as u32).unwrap_or(0));
            n = 0;
        }
        Some(IPMask(out))
    }

    /// `mask.Size()`: (ones, bits), or (0, 0) for a non-canonical mask.
    pub fn size(&self) -> (usize, usize) {
        match simple_mask_length(&self.0) {
            Some(ones) => (ones, self.0.len() * 8),
            None => (0, 0),
        }
    }
}

impl fmt::Display for IPMask {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.0.is_empty() {
            return f.write_str("<nil>");
        }
        for b in &self.0 {
            write!(f, "{:02x}", b)?;
        }
        Ok(())
    }
}

fn simple_mask_length(mask: &[u8]) -> Option<usize> {
    let mut ones = 0;
    let mut seen_zero = false;
    for &b in mask {
        if seen_zero {
            if b != 0 {
                return None;
            }
            continue;
        }
        if b == 0xff {
            ones += 8;
            continue;
        }
        let lead = b.leading_ones() as usize;
        if b << lead != 0 {
            return None;
        }
        ones += lead;
        seen_zero = true;
    }
    Some(ones)
}

/// A Go `*net.IPNet`.
#[derive(Clone, PartialEq, Eq, Hash, Debug)]
pub struct IPNet {
    pub ip: IP,
    pub mask: IPMask,
}

impl IPNet {
    fn network_number_and_mask(&self) -> Option<(IP, Vec<u8>)> {
        let ip = match self.ip.to4() {
            Some(v4) => v4,
            None => {
                if self.ip.len() != 16 {
                    return None;
                }
                self.ip.clone()
            }
        };
        let mut m = self.mask.0.clone();
        match m.len() {
            4 => {
                if ip.len() != 4 {
                    return None;
                }
            }
            16 => {
                if ip.len() == 4 {
                    m = m[12..].to_vec();
                }
            }
            _ => return None,
        }
        Some((ip, m))
    }

    /// `n.Contains(ip)`.
    pub fn contains(&self, ip: &IP) -> bool {
        let Some((nn, m)) = self.network_number_and_mask() else {
            return false;
        };
        let ip = ip.to4().unwrap_or_else(|| ip.clone());
        if ip.len() != nn.len() {
            return false;
        }
        (0..ip.len()).all(|i| nn.0[i] & m[i] == ip.0[i] & m[i])
    }

    pub fn contains_opt(&self, ip: Option<&IP>) -> bool {
        ip.is_some_and(|ip| self.contains(ip))
    }

    /// The broadcast address as computed by the Go validators:
    /// `network.IP.To4()[i] | ^network.Mask[i]`.
    pub fn ipv4_broadcast(&self) -> Option<IP> {
        let network = self.ip.to4()?;
        let mask = self.mask.bytes();
        if mask.len() < 4 {
            return None;
        }
        Some(IP((0..4).map(|i| network.0[i] | !mask[i]).collect()))
    }
}

impl fmt::Display for IPNet {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let Some((nn, m)) = self.network_number_and_mask() else {
            return f.write_str("<nil>");
        };
        match simple_mask_length(&m) {
            Some(ones) => write!(f, "{}/{}", nn, ones),
            None => write!(f, "{}/{}", nn, IPMask(m)),
        }
    }
}

/// Go's `dtoi`: decimal digits from the start of `s`; (value, consumed, ok).
fn dtoi(s: &str) -> (i64, usize, bool) {
    const BIG: i64 = 0xFFFFFF;
    let mut n: i64 = 0;
    let mut i = 0;
    for b in s.bytes() {
        if !b.is_ascii_digit() {
            break;
        }
        n = n * 10 + (b - b'0') as i64;
        if n >= BIG {
            return (BIG, i, false);
        }
        i += 1;
    }
    if i == 0 {
        return (0, 0, false);
    }
    (n, i, true)
}

/// `net.ParseCIDR`: returns the full address and the masked network.
pub fn parse_cidr(value: &str) -> Option<(IP, IPNet)> {
    let (addr, mask) = value.split_once('/')?;
    if addr.contains('%') {
        return None;
    }
    // netip.ParseAddr distinguishes a plain IPv4 address (32-bit prefix) from
    // an IPv4-mapped IPv6 spelling (128-bit prefix).
    let (ip16, bits) = if let Ok(v4) = parse_ipv4_strict(addr) {
        let o = v4.octets();
        (IP::v4(o[0], o[1], o[2], o[3]), 32)
    } else if addr.contains(':') {
        let v6: Ipv6Addr = addr.parse().ok()?;
        (IP(v6.octets().to_vec()), 128)
    } else {
        return None;
    };
    let (n, i, ok) = dtoi(mask);
    if !ok || i != mask.len() || n < 0 || n as usize > bits {
        return None;
    }
    let m = IPMask::cidr(n as usize, bits)?;
    let network_ip = ip16.mask(&m)?;
    Some((ip16, IPNet { ip: network_ip, mask: m }))
}

/// `net.ParseMAC` restricted to the value; returns the hardware address bytes.
pub fn parse_mac(s: &str) -> Option<Vec<u8>> {
    let b = s.as_bytes();
    if b.len() < 14 {
        return None;
    }
    if b[2] == b':' || b[2] == b'-' {
        if (b.len() + 1) % 3 != 0 {
            return None;
        }
        let n = (b.len() + 1) / 3;
        if n != 6 && n != 8 && n != 20 {
            return None;
        }
        let mut hw = vec![0u8; n];
        let mut x = 0;
        for item in hw.iter_mut() {
            *item = xtoi2(&b[x..], b[2])?;
            x += 3;
        }
        return Some(hw);
    }
    if b[4] == b'.' {
        if (b.len() + 1) % 5 != 0 {
            return None;
        }
        let n = 2 * (b.len() + 1) / 5;
        if n != 6 && n != 8 && n != 20 {
            return None;
        }
        let mut hw = vec![0u8; n];
        let mut x = 0;
        let mut i = 0;
        while i < n {
            hw[i] = xtoi2(&b[x..x + 2], 0)?;
            hw[i + 1] = xtoi2(&b[x + 2..], b[4])?;
            x += 5;
            i += 2;
        }
        return Some(hw);
    }
    None
}

fn xtoi2(s: &[u8], sep: u8) -> Option<u8> {
    if s.len() > 2 && s[2] != sep {
        return None;
    }
    if s.len() < 2 {
        return None;
    }
    let hi = (s[0] as char).to_digit(16)?;
    let lo = (s[1] as char).to_digit(16)?;
    Some((hi * 16 + lo) as u8)
}

/// `HardwareAddr.String()`.
pub fn mac_string(hw: &[u8]) -> String {
    hw.iter().map(|b| format!("{:02x}", b)).collect::<Vec<_>>().join(":")
}

/// `net.SplitHostPort`.
pub fn split_host_port(hostport: &str) -> Result<(String, String), String> {
    let addr_err = |why: &str| Err(format!("address {}: {}", hostport, why));
    let b = hostport.as_bytes();
    let Some(i) = hostport.rfind(':') else {
        return addr_err("missing port in address");
    };
    let (host, j, k);
    if b[0] == b'[' {
        let Some(end) = hostport.find(']') else {
            return addr_err("missing ']' in address");
        };
        if end + 1 == hostport.len() {
            return addr_err("missing port in address");
        } else if end + 1 != i {
            if b[end + 1] == b':' {
                return addr_err("too many colons in address");
            }
            return addr_err("missing port in address");
        }
        host = &hostport[1..end];
        j = 1;
        k = end + 1;
    } else {
        host = &hostport[..i];
        if host.contains(':') {
            return addr_err("too many colons in address");
        }
        j = 0;
        k = 0;
    }
    if hostport[j..].contains('[') {
        return addr_err("unexpected '[' in address");
    }
    if hostport[k..].contains(']') {
        return addr_err("unexpected ']' in address");
    }
    Ok((host.to_string(), hostport[i + 1..].to_string()))
}

/// `net.JoinHostPort`.
pub fn join_host_port(host: &str, port: &str) -> String {
    if host.contains(':') {
        format!("[{}]:{}", host, port)
    } else {
        format!("{}:{}", host, port)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_ip_matches_go() {
        assert!(parse_ip("192.168.1.1").is_some());
        assert!(parse_ip("192.168.01.1").is_none());
        assert!(parse_ip("256.1.1.1").is_none());
        assert!(parse_ip("1.2.3").is_none());
        assert!(parse_ip("fe80::1%eth0").is_none());
        let mapped = parse_ip("::ffff:10.0.0.1").unwrap();
        assert_eq!(mapped.to4().unwrap().to_string(), "10.0.0.1");
        assert_eq!(parse_ip("2001:db8::1").unwrap().to_string(), "2001:db8::1");
    }

    #[test]
    fn parse_cidr_matches_go() {
        let (ip, net) = parse_cidr("192.168.1.1/24").unwrap();
        assert_eq!(ip.to_string(), "192.168.1.1");
        assert_eq!(net.to_string(), "192.168.1.0/24");
        assert_eq!(net.mask.size(), (24, 32));
        assert_eq!(net.ip.len(), 4);
        assert!(parse_cidr("192.168.1.1/33").is_none());
        assert!(parse_cidr("192.168.1.1").is_none());
        assert!(parse_cidr("192.168.1.1/024").is_some());
        assert!(parse_cidr("192.168.1.1/").is_none());
        let (_, v6) = parse_cidr("::/0").unwrap();
        assert_eq!(v6.mask.size(), (0, 128));
        assert_eq!(v6.to_string(), "::/0");
        let (_, mapped) = parse_cidr("::ffff:10.0.0.1/104").unwrap();
        assert_eq!(mapped.mask.size(), (104, 128));
        assert_eq!(IP::from_bytes(mapped.mask.bytes()).to_string().is_empty(), false);
    }

    #[test]
    fn contains_and_broadcast() {
        let (_, net) = parse_cidr("10.0.0.0/8").unwrap();
        assert!(net.contains(&parse_ip("10.1.2.3").unwrap()));
        assert!(!net.contains(&parse_ip("11.1.2.3").unwrap()));
        assert_eq!(net.ipv4_broadcast().unwrap().to_string(), "10.255.255.255");
        assert_eq!(IP::from_bytes(net.mask.bytes()).to_string(), "255.0.0.0");
    }

    #[test]
    fn mac_and_hostport() {
        assert_eq!(mac_string(&parse_mac("AA-bb-cc-dd-ee-ff").unwrap()), "aa:bb:cc:dd:ee:ff");
        assert_eq!(mac_string(&parse_mac("aabb.ccdd.eeff").unwrap()), "aa:bb:cc:dd:ee:ff");
        assert!(parse_mac("aa:bb:cc:dd:ee").is_none());
        assert!(parse_mac("aa:bb-cc:dd:ee:ff").is_none());
        assert_eq!(split_host_port("1.2.3.4:51820").unwrap(), ("1.2.3.4".into(), "51820".into()));
        assert_eq!(split_host_port("[::1]:80").unwrap(), ("::1".into(), "80".into()));
        assert!(split_host_port("::1:80").is_err());
        assert!(split_host_port("host").is_err());
        assert_eq!(split_host_port(":80").unwrap(), ("".into(), "80".into()));
    }
}
