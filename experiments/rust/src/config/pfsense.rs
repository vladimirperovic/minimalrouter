//! pfSense `config.xml` import with an explicit Linux interface mapping.

use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

use super::model::*;
use super::validation::{parse_ipv4, valid_interface_name};
use crate::errorf;
use crate::goish::errors::{wrapf, Result};
use crate::goish::json::List;
use crate::goish::net::{IPMask, IP};
use crate::goish::strings::{quote, to_lower};
use crate::goish::time::Time;

const MAX_PFSENSE_XML_BYTES: usize = 8 << 20;

/// Mandatory because pfSense/FreeBSD names such as em0 or igb1 are not stable
/// Linux interface identifiers on the target.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PfSenseInterfaceMapping {
    pub wan: String,
    pub lan: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct PfSenseImportReport {
    pub config: SystemConfig,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub source_version: String,
    pub warnings: List<String>,
    pub unsupported_sections: List<String>,
    pub imported: BTreeMap<String, i64>,
}

/// The subset of `encoding/xml` decoding the importer relies on: a string
/// field takes the direct character data of the last matching child element.
fn child_text(node: roxmltree::Node<'_, '_>, name: &str) -> String {
    node.children()
        .filter(|c| c.is_element() && c.tag_name().name() == name)
        .last()
        .map(|c| c.children().filter(|t| t.is_text()).map(|t| t.text().unwrap_or("")).collect())
        .unwrap_or_default()
}

fn children<'a, 'input>(node: roxmltree::Node<'a, 'input>, name: &'a str) -> impl Iterator<Item = roxmltree::Node<'a, 'input>> + 'a {
    node.children().filter(move |c| c.is_element() && c.tag_name().name() == name)
}

/// Repeated struct elements are decoded into the same value and each string
/// occurrence overwrites the previous one, so the last element matching the
/// whole path in document order wins.
fn nested_text(node: roxmltree::Node<'_, '_>, path: &[&str]) -> String {
    let mut nodes = vec![node];
    for name in path {
        nodes = nodes.into_iter().flat_map(|n| children(n, name).collect::<Vec<_>>()).collect();
    }
    nodes
        .last()
        .map(|n| n.children().filter(|t| t.is_text()).map(|t| t.text().unwrap_or("")).collect())
        .unwrap_or_default()
}

/// `fmt.Sscanf(text, "%d", &n)`: skips leading spaces, optional sign, digits.
fn sscan_int(text: &str) -> Option<i64> {
    let trimmed = text.trim_start_matches([' ', '\t', '\r']);
    let bytes = trimmed.as_bytes();
    let mut end = 0;
    if end < bytes.len() && (bytes[end] == b'+' || bytes[end] == b'-') {
        end += 1;
    }
    let digits_start = end;
    while end < bytes.len() && (bytes[end].is_ascii_digit() || bytes[end] == b'_') {
        end += 1;
    }
    if end == digits_start {
        return None;
    }
    trimmed[..end].parse::<i64>().ok()
}

/// Parses a pfSense config.xml using an explicit target interface mapping.
/// Unsupported settings are reported rather than silently dropped.
pub fn import_pfsense_xml_with_mapping(xml_content: &[u8], mapping: &PfSenseInterfaceMapping) -> Result<PfSenseImportReport, (PfSenseImportReport, crate::goish::errors::Error)> {
    let fail = |e: crate::goish::errors::Error| (PfSenseImportReport::default(), e);
    if xml_content.is_empty() || xml_content.len() > MAX_PFSENSE_XML_BYTES {
        return Err(fail(errorf!("pfSense XML must be between 1 byte and {} bytes", MAX_PFSENSE_XML_BYTES)));
    }
    if !valid_interface_name(&mapping.wan) || !valid_interface_name(&mapping.lan) || mapping.wan == mapping.lan {
        return Err(fail(errorf!("valid, distinct target WAN and LAN interfaces are required")));
    }
    let text = std::str::from_utf8(xml_content).map_err(|e| fail(wrapf("failed to parse pfSense XML", e)))?;
    let doc = roxmltree::Document::parse(text).map_err(|e| fail(wrapf("failed to parse pfSense XML", e)))?;
    let root = doc.root_element();
    if root.tag_name().name() != "pfsense" {
        return Err(fail(errorf!("failed to parse pfSense XML: expected element type <pfsense> but have <{}>", root.tag_name().name())));
    }

    let mut cfg = default_config();
    cfg.updated_at = Time::now();
    cfg.wan.interface = mapping.wan.clone();
    cfg.lan.interface = mapping.lan.clone();
    let mut report = PfSenseImportReport {
        source_version: child_text(root, "version"),
        warnings: List::empty(),
        unsupported_sections: List::empty(),
        imported: BTreeMap::new(),
        ..Default::default()
    };

    // 1. System metadata.
    let hostname = nested_text(root, &["system", "hostname"]);
    if !hostname.is_empty() {
        cfg.system.hostname = hostname;
    }
    let domain = nested_text(root, &["system", "domain"]);
    if !domain.is_empty() {
        cfg.system.domain = domain;
    }

    // 2. Interfaces. Source identifiers are informational only.
    let lan_ip = nested_text(root, &["interfaces", "lan", "ipaddr"]);
    if !lan_ip.is_empty() {
        cfg.lan.ip_address = lan_ip.clone();
        let mut subnet = nested_text(root, &["interfaces", "lan", "subnet"]);
        if subnet.is_empty() {
            subnet = "24".into();
        }
        cfg.lan.cidr = format!("{}/{}", lan_ip, subnet);
        if let Some(prefix) = sscan_int(&subnet) {
            if (0..=32).contains(&prefix) {
                if let Some(mask) = IPMask::cidr(prefix as usize, 32) {
                    cfg.lan.netmask = IP::from_bytes(mask.bytes()).to_string();
                }
            }
        }
    }

    // 3. PPPoE credentials.
    let ppps: Vec<_> = children(root, "ppps").flat_map(|p| children(p, "ppp").collect::<Vec<_>>()).collect();
    if let Some(first) = ppps.first() {
        cfg.wan.enabled = true;
        cfg.wan.username = child_text(*first, "username");
        cfg.wan.password = child_text(*first, "password");
        report.imported.insert("pppoe_accounts".into(), 1);
        if ppps.len() > 1 {
            report.warnings.push("Only the first pfSense PPP connection was imported.".into());
            report.unsupported_sections.push("additional PPP connections".into());
        }
    }

    // 4. DHCP server and static leases.
    let dhcp_lans: Vec<_> = children(root, "dhcpd").flat_map(|d| children(d, "lan").collect::<Vec<_>>()).collect();
    let enable = nested_text(root, &["dhcpd", "lan", "enable"]);
    let range_from = nested_text(root, &["dhcpd", "lan", "range", "from"]);
    if !enable.is_empty() || !range_from.is_empty() {
        cfg.dhcp.enabled = true;
        cfg.dhcp.range_start = range_from;
        cfg.dhcp.range_end = nested_text(root, &["dhcpd", "lan", "range", "to"]);
        let mut leases: List<StaticLease> = List::nil();
        for (i, sm) in dhcp_lans.iter().flat_map(|l| children(*l, "staticmap").collect::<Vec<_>>()).enumerate() {
            leases.push(StaticLease {
                id: format!("pf-static-{}", i + 1),
                hostname: child_text(sm, "hostname"),
                mac: child_text(sm, "mac"),
                ip_address: child_text(sm, "ipaddr"),
            });
        }
        report.imported.insert("dhcp_static_leases".into(), leases.len() as i64);
        cfg.dhcp.static_leases = leases;
    }

    // 5. Port forwarding rules.
    let mut port_forwards: List<PortForwardRule> = List::nil();
    let rules: Vec<_> = children(root, "nat").flat_map(|n| children(n, "rule").collect::<Vec<_>>()).collect();
    for (i, r) in rules.iter().enumerate() {
        let descr = child_text(*r, "descr");
        let iface = child_text(*r, "interface");
        let protocol = child_text(*r, "protocol");
        let target = child_text(*r, "target");
        let local_port = child_text(*r, "local-port");
        // Go reads <disabled/> as an empty string, so only a non-empty marker
        // disables a rule; this mirrors that behaviour exactly.
        if !child_text(*r, "disabled").is_empty() {
            continue;
        }
        let mut external_port = child_text(*r, "external-port");
        if external_port.is_empty() {
            external_port = nested_text(*r, &["destination", "port"]);
        }
        let ext_port = sscan_int(&external_port).unwrap_or(0);
        let mut int_port = sscan_int(&local_port).unwrap_or(0);

        if !iface.is_empty() && iface != "wan" {
            report.warnings.push(format!("Skipped NAT rule {} because only WAN port forwards are supported.", quote(&descr)));
            continue;
        }
        if external_port.contains('-') || local_port.contains('-') {
            report.warnings.push(format!("Skipped NAT rule {} because port ranges are not supported.", quote(&descr)));
            continue;
        }
        if ext_port > 0 && int_port == 0 {
            int_port = ext_port;
        }
        // pfSense writes "tcp/udp" for the dual-protocol forward; that is
        // this model's "both". Other protocols have no port to forward.
        let proto = match to_lower(protocol.trim()).as_str() {
            "" | "tcp" => "tcp".to_string(),
            "udp" => "udp".to_string(),
            "tcp/udp" => "both".to_string(),
            _ => {
                report.warnings.push(format!(
                    "Skipped NAT rule {} because protocol {} cannot be port-forwarded.",
                    quote(&descr),
                    quote(&protocol)
                ));
                continue;
            }
        };
        // Targets may be aliases or host names, which have no equivalent.
        if !target.is_empty() && parse_ipv4(&target).is_none() {
            report.warnings.push(format!(
                "Skipped NAT rule {} because its target {} is not an IPv4 address.",
                quote(&descr),
                quote(&target)
            ));
            continue;
        }
        if ext_port > 0 && int_port > 0 && !target.is_empty() {
            let name = if descr.is_empty() { format!("Rule {}", i + 1) } else { descr.clone() };
            port_forwards.push(PortForwardRule {
                id: format!("pf-nat-{}", i + 1),
                name: name.clone(),
                protocol: proto,
                external_port: ext_port,
                internal_ip: target,
                internal_port: int_port,
                enabled: false,
            });
            report.warnings.push(format!(
                "Imported NAT rule {} as disabled because WireGuard is the only permitted WAN entry point.",
                quote(&name)
            ));
        } else {
            report.warnings.push(format!("Skipped NAT rule {} because its target or ports could not be represented.", quote(&descr)));
        }
    }
    report.imported.insert("port_forwards".into(), port_forwards.len() as i64);
    cfg.firewall.port_forwards = port_forwards;

    let count = |section: &str, item: &str| children(root, section).map(|s| children(s, item).count()).sum::<usize>();
    let filter_rules = count("filter", "rule");
    if filter_rules > 0 {
        report.unsupported_sections.push("firewall filter rules".into());
        report.warnings.push(format!("{} pfSense firewall rules require manual review and were not imported.", filter_rules));
    }
    let aliases = count("aliases", "alias");
    if aliases > 0 {
        report.unsupported_sections.push("aliases".into());
        report.warnings.push(format!("{} pfSense aliases require manual review and were not imported.", aliases));
    }
    let vlans = count("vlans", "vlan");
    if vlans > 0 {
        report.unsupported_sections.push("VLANs".into());
        report.warnings.push(format!("{} pfSense VLANs require manual interface design and were not imported.", vlans));
    }
    report.unsupported_sections.sort();

    let validation = cfg.validate();
    report.config = cfg;
    if let Err(err) = validation {
        return Err((report, wrapf("imported configuration is not safe to apply", err)));
    }
    Ok(report)
}

/// Retained for library callers; uses the safe Linux defaults instead of
/// copying pfSense interface names.
pub fn import_pfsense_xml(xml_content: &[u8]) -> Result<SystemConfig, (SystemConfig, crate::goish::errors::Error)> {
    let mapping = PfSenseInterfaceMapping { wan: "eth0".into(), lan: "eth1".into() };
    import_pfsense_xml_with_mapping(xml_content, &mapping).map(|r| r.config).map_err(|(r, e)| (r.config, e))
}
