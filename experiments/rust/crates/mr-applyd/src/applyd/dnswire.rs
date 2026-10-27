//! Minimal DNS wire format for the helper's local resolver probes.

use std::net::UdpSocket;
use std::time::Duration;

use crate::errorf;
use crate::goish::errors::Result;

pub(crate) const TYPE_A: u16 = 1;
pub(crate) const TYPE_TXT: u16 = 16;

pub(crate) fn encode_name(name: &str, out: &mut Vec<u8>) -> Result<()> {
    for label in name.trim_end_matches('.').split('.').filter(|l| !l.is_empty()) {
        if label.len() > 63 {
            return Err(errorf!("DNS label too long"));
        }
        out.push(label.len() as u8);
        out.extend_from_slice(label.as_bytes());
    }
    out.push(0);
    Ok(())
}

pub(crate) fn build_query(id: u16, name: &str, qtype: u16, recursion: bool) -> Result<Vec<u8>> {
    let mut packet = Vec::with_capacity(64);
    packet.extend_from_slice(&id.to_be_bytes());
    packet.extend_from_slice(&[if recursion { 1 } else { 0 }, 0, 0, 1, 0, 0, 0, 0, 0, 0]);
    encode_name(name, &mut packet)?;
    packet.extend_from_slice(&qtype.to_be_bytes());
    packet.extend_from_slice(&1u16.to_be_bytes());
    Ok(packet)
}

/// Reads a possibly compressed name starting at `offset`; returns it and the
/// offset just past the name in the original position.
fn read_name(packet: &[u8], mut offset: usize) -> Result<(String, usize)> {
    let mut labels = Vec::new();
    let mut end = None;
    let mut jumps = 0;
    loop {
        let len = *packet.get(offset).ok_or_else(|| errorf!("truncated DNS name"))? as usize;
        if len & 0xc0 == 0xc0 {
            let lo = *packet.get(offset + 1).ok_or_else(|| errorf!("truncated DNS pointer"))? as usize;
            if end.is_none() {
                end = Some(offset + 2);
            }
            jumps += 1;
            if jumps > 32 {
                return Err(errorf!("DNS name pointer loop"));
            }
            offset = ((len & 0x3f) << 8) | lo;
            continue;
        }
        if len == 0 {
            let next = end.unwrap_or(offset + 1);
            return Ok((format!("{}.", labels.join(".")), next));
        }
        let label = packet.get(offset + 1..offset + 1 + len).ok_or_else(|| errorf!("truncated DNS label"))?;
        labels.push(String::from_utf8_lossy(label).to_ascii_lowercase());
        offset += 1 + len;
    }
}

pub(crate) struct Answer {
    pub name: String,
    pub rtype: u16,
    pub data: Vec<u8>,
}

pub(crate) struct Message {
    pub id: u16,
    pub response: bool,
    pub rcode: u8,
    pub answers: Vec<Answer>,
}

pub(crate) fn parse(packet: &[u8]) -> Result<Message> {
    if packet.len() < 12 {
        return Err(errorf!("short DNS message"));
    }
    let u16_at = |i: usize| u16::from_be_bytes([packet[i], packet[i + 1]]);
    let id = u16_at(0);
    let response = packet[2] & 0x80 != 0;
    let rcode = packet[3] & 0x0f;
    let questions = u16_at(4) as usize;
    let answers = u16_at(6) as usize;
    let mut offset = 12;
    for _ in 0..questions {
        let (_, next) = read_name(packet, offset)?;
        offset = next + 4;
    }
    let mut out = Vec::new();
    for _ in 0..answers {
        let (name, next) = read_name(packet, offset)?;
        let header = packet.get(next..next + 10).ok_or_else(|| errorf!("truncated DNS answer"))?;
        let rtype = u16::from_be_bytes([header[0], header[1]]);
        let rdlen = u16::from_be_bytes([header[8], header[9]]) as usize;
        let data = packet.get(next + 10..next + 10 + rdlen).ok_or_else(|| errorf!("truncated DNS rdata"))?.to_vec();
        offset = next + 10 + rdlen;
        out.push(Answer { name, rtype, data });
    }
    Ok(Message { id, response, rcode, answers: out })
}

/// The concatenated character-strings of a TXT record.
pub(crate) fn txt_strings(data: &[u8]) -> String {
    let mut out = String::new();
    let mut i = 0;
    while i < data.len() {
        let len = data[i] as usize;
        let end = (i + 1 + len).min(data.len());
        out.push_str(&String::from_utf8_lossy(&data[i + 1..end]));
        i = end;
    }
    out
}

/// One UDP exchange with a bounded timeout.
pub(crate) fn exchange(address: &str, packet: &[u8], timeout: Duration) -> Result<Vec<u8>> {
    let socket = UdpSocket::bind("0.0.0.0:0")?;
    socket.set_read_timeout(Some(timeout))?;
    socket.set_write_timeout(Some(timeout))?;
    socket.connect(address)?;
    socket.send(packet)?;
    let mut buf = vec![0u8; 4096];
    let n = socket.recv(&mut buf)?;
    buf.truncate(n);
    Ok(buf)
}

/// Resolves `name` to IPv4 addresses through one resolver.
pub(crate) fn lookup_a(address: &str, name: &str, timeout: Duration) -> Result<Vec<std::net::Ipv4Addr>> {
    let id = u16::from_be_bytes([crate::goish::rand::bytes(2).map_err(|e| errorf!("{}", e))?[0], 7]);
    let reply = parse(&exchange(address, &build_query(id, name, TYPE_A, true)?, timeout)?)?;
    if reply.id != id || !reply.response {
        return Err(errorf!("invalid DNS response"));
    }
    if reply.rcode != 0 {
        return Err(errorf!("lookup {} on {}: rcode {}", name, address, reply.rcode));
    }
    let addrs: Vec<_> = reply.answers.iter().filter(|a| a.rtype == TYPE_A && a.data.len() == 4).map(|a| std::net::Ipv4Addr::new(a.data[0], a.data[1], a.data[2], a.data[3])).collect();
    if addrs.is_empty() {
        return Err(errorf!("lookup {} on {}: no such host", name, address));
    }
    Ok(addrs)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn query_round_trips_through_parser() {
        let q = build_query(0x1234, "filter-health.minimalrouter.invalid.", TYPE_TXT, false).unwrap();
        let mut reply = q.clone();
        reply[2] = 0x84;
        reply[7] = 1;
        reply.extend_from_slice(&[0xc0, 12, 0, 16, 0, 1, 0, 0, 0, 0, 0, 3, 2, b'4', b'2']);
        let msg = parse(&reply).unwrap();
        assert_eq!(msg.id, 0x1234);
        assert!(msg.response);
        assert_eq!(msg.answers[0].name, "filter-health.minimalrouter.invalid.");
        assert_eq!(txt_strings(&msg.answers[0].data), "42");
    }
}
