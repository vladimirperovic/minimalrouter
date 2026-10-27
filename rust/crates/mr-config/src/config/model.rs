use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

use crate::goish::json::List;
use crate::goish::time::Time;
use crate::list;

/// An optimistic concurrency token for config changes.
pub type Revision = u64;

/// The complete canonical configuration model.
///
/// Field order and JSON names match the Go struct exactly: the encoded form is
/// persisted, hashed and compared byte for byte.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SystemConfig {
    pub revision: Revision,
    pub updated_at: Time,
    pub system: SystemSettings,
    pub wan: WANSettings,
    pub lan: LANSettings,
    pub dhcp: DHCPSettings,
    pub dns: DNSSettings,
    pub firewall: FirewallConfig,
    pub wireguard: WireGuardConfig,
    /// The outbound WireGuard tunnel (client mode) used to reach remote sites
    /// such as an office network. nftables only accepts established traffic
    /// from this interface, so the remote site can never open a connection
    /// back into the home network.
    pub wg_client: WGClientConfig,
    pub cloudflare: CloudflareConfig,
    pub squid_proxy: SquidProxyConfig,
    /// The retained JSON compatibility key. The user-facing feature is named
    /// DNS Filter and does not embed or impersonate AdGuard Home.
    pub adguard: DNSFilterConfig,
    pub qos: QoSConfig,
    pub wifi: WiFiConfig,
    /// Per-device byte counting in the forward chain; opt-in.
    pub accounting: AccountingConfig,
    /// Restricts administrative Web UI/API access to clients whose source
    /// address falls within one of the listed CIDR networks. Localhost is
    /// always trusted. It does not replace authentication.
    pub trusted_networks: List<String>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WireGuardConfig {
    pub enabled: bool,
    pub interface: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub private_key: String,
    pub listen_port: i64,
    pub address: String,
    pub peers: List<WireGuardPeer>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WireGuardPeer {
    pub id: String,
    pub name: String,
    pub public_key: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub preshared_key: String,
    pub allowed_ips: List<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub endpoint: String,
    pub enabled: bool,
}

/// The outbound WireGuard tunnel (wg1). This router dials the remote endpoint;
/// AllowedIPs lists the remote networks reachable through the tunnel.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WGClientConfig {
    pub enabled: bool,
    pub interface: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub private_key: String,
    pub address: String,
    pub public_key: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub preshared_key: String,
    pub endpoint: String,
    pub allowed_ips: List<String>,
    pub persistent_keepalive: i64,
}

/// Retains its historical JSON key so existing backups remain compatible. DDNS
/// and Cloudflare Tunnel keep separate hostname and secret fields so saving
/// either integration cannot mutate the other.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct CloudflareConfig {
    pub ddns_enabled: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub ddns_provider: String,
    #[serde(rename = "ddns_username", skip_serializing_if = "String::is_empty")]
    pub ddns_user: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub api_token: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub zone_id: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub zone_name: String,
    pub domain: String,
    pub tunnel_enabled: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub tunnel_hostname: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub tunnel_token: String,
}

/// hostapd Wi-Fi access point settings.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WiFiConfig {
    pub enabled: bool,
    pub interface: String,
    pub ssid: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub passphrase: String,
    pub band: String,
    pub channel: i64,
    pub hide_ssid: bool,
}

pub const WIFI_BRIDGE_INTERFACE: &str = "br-lan";

impl SystemConfig {
    /// The interface that owns the LAN address and receives LAN firewall/DHCP
    /// policy. Wi-Fi clients join the same LAN through a bridge.
    pub fn runtime_lan_interface(&self) -> &str {
        if self.wifi.enabled { WIFI_BRIDGE_INTERFACE } else { &self.lan.interface }
    }
}

/// Traffic shaping and CAKE / FQ-CoDel bufferbloat prevention.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct QoSConfig {
    pub enabled: bool,
    pub algorithm: String,
    pub download_limit_mbps: i64,
    pub upload_limit_mbps: i64,
}

/// A local-router-time half-open access interval. End must be later than
/// Start; midnight-spanning windows are represented as two windows.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct AccessWindow {
    pub start: String,
    pub end: String,
}

/// Controls managed services independently for each day. The legacy
/// weekday/weekend fields remain readable for old backups.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WeeklyAccessSchedule {
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub day_windows: BTreeMap<String, List<AccessWindow>>,
    #[serde(skip_serializing_if = "List::is_empty")]
    pub weekday_windows: List<AccessWindow>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub weekend_mode: String,
    #[serde(skip_serializing_if = "List::is_empty")]
    pub weekend_windows: List<AccessWindow>,
}

/// Applies a service schedule to one or more static LAN addresses.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DeviceProfile {
    pub id: String,
    pub name: String,
    pub ip_addresses: List<String>,
    pub services: List<String>,
    pub schedule: WeeklyAccessSchedule,
    pub enabled: bool,
}

/// Retained only so old backups can be decoded.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FilterDeviceRule {
    pub id: String,
    pub hostname: String,
    pub ip_address: String,
    pub blocked_services: List<String>,
    pub enabled: bool,
}

/// The global DNS sinkhole and per-device access profiles.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSFilterConfig {
    pub enabled: bool,
    pub blocklist_url: String,
    pub last_updated: String,
    #[serde(skip_serializing_if = "List::is_empty")]
    pub filter_devices: List<FilterDeviceRule>,
    pub device_profiles: List<DeviceProfile>,
}

pub type AdGuardConfig = DNSFilterConfig;

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RestrictedIPItem {
    pub hostname: String,
    pub ip_address: String,
    pub enabled: bool,
}

/// Non-caching HTTP/HTTPS proxy configuration.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SquidProxyConfig {
    pub enabled: bool,
    pub port: i64,
    pub username: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub password: String,
    pub restricted_ips: List<RestrictedIPItem>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct SystemSettings {
    pub hostname: String,
    pub domain: String,
    pub https_enabled: bool,
    pub https_port: i64,
    pub management_access: String,
}

/// PPPoE internet connection configuration.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct WANSettings {
    pub interface: String,
    pub enabled: bool,
    pub username: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub password: String,
    pub mtu: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LANSettings {
    pub interface: String,
    pub ip_address: String,
    pub netmask: String,
    pub cidr: String,
}

/// dnsmasq DHCP server configuration and static leases.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DHCPSettings {
    pub enabled: bool,
    pub dns_enabled: bool,
    pub range_start: String,
    pub range_end: String,
    pub lease_time: String,
    pub dns_servers: List<String>,
    pub static_leases: List<StaticLease>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct StaticLease {
    pub id: String,
    pub hostname: String,
    pub mac: String,
    pub ip_address: String,
}

/// Static DNS records served by the local resolver, independent of DHCP.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSSettings {
    #[serde(skip_serializing_if = "List::is_empty")]
    pub records: List<DNSRecord>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSRecord {
    pub name: String,
    pub ip: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FirewallConfig {
    pub default_wan_input_policy: String,
    pub wan_ingress_mode: String,
    pub stateful_firewall: bool,
    pub port_forwards: List<PortForwardRule>,
    pub custom_rules: List<FirewallRule>,
    #[serde(skip_serializing_if = "List::is_empty")]
    pub extra_lans: List<ExtraLANConfig>,
}

/// An additional isolated LAN segment. Only hosts inside AllowFrom CIDRs may
/// reach the single service DstIP:DstPort; the segment has no WAN/LAN egress
/// and hosts no router services.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ExtraLANConfig {
    pub id: String,
    pub name: String,
    pub interface: String,
    pub cidr: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub router_address: String,
    pub dst_ip: String,
    pub dst_port: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub protocol: String,
    pub allow_from: List<String>,
    pub enabled: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PortForwardRule {
    pub id: String,
    pub name: String,
    pub protocol: String,
    pub external_port: i64,
    pub internal_ip: String,
    pub internal_port: i64,
    pub enabled: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FirewallRule {
    pub id: String,
    pub name: String,
    pub action: String,
    pub direction: String,
    pub protocol: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub src_ip: String,
    #[serde(skip_serializing_if = "crate::goish::json::is_zero_i64")]
    pub dst_port: i64,
    pub enabled: bool,
    /// Empty scope preserves existing forward-deny behaviour across all zones.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub scope: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub preset_id: String,
}

/// Per-device traffic accounting. Only aggregate byte counts per LAN address
/// are recorded.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AccountingConfig {
    pub enabled: bool,
    pub retention_months: i64,
}

impl SystemConfig {
    /// A fully detached copy. Rust's `Clone` already deep-copies every owned
    /// collection and keeps nil-ness, which the Go version had to do by hand.
    pub fn deep_copy(&self) -> SystemConfig {
        self.clone()
    }
}

/// A secure default Minimal Router configuration.
pub fn default_config() -> SystemConfig {
    SystemConfig {
        revision: 1,
        updated_at: Time::now(),
        system: SystemSettings {
            hostname: "minimalrouter".into(),
            domain: "lan".into(),
            https_enabled: true,
            https_port: 8443,
            management_access: "lan_and_wireguard".into(),
        },
        wan: WANSettings { interface: "eth0".into(), enabled: false, mtu: 1492, ..Default::default() },
        lan: LANSettings { interface: "eth1".into(), ip_address: "192.168.1.1".into(), netmask: "255.255.255.0".into(), cidr: "192.168.1.1/24".into() },
        dhcp: DHCPSettings {
            enabled: true,
            range_start: "192.168.1.100".into(),
            range_end: "192.168.1.200".into(),
            lease_time: "12h".into(),
            dns_servers: list!["1.1.1.1".to_string(), "1.0.0.1".to_string()],
            static_leases: list![],
            ..Default::default()
        },
        firewall: FirewallConfig {
            default_wan_input_policy: "deny".into(),
            wan_ingress_mode: "wireguard_only".into(),
            stateful_firewall: true,
            port_forwards: list![],
            custom_rules: list![],
            ..Default::default()
        },
        wireguard: WireGuardConfig {
            enabled: false,
            interface: "wg0".into(),
            listen_port: 51820,
            address: "10.8.0.1/24".into(),
            peers: list![],
            ..Default::default()
        },
        wg_client: WGClientConfig { enabled: false, interface: "wg1".into(), persistent_keepalive: 25, allowed_ips: list![], ..Default::default() },
        cloudflare: CloudflareConfig { ddns_provider: "noip".into(), ..Default::default() },
        squid_proxy: SquidProxyConfig { enabled: false, port: 3128, username: "proxyadmin".into(), restricted_ips: list![], ..Default::default() },
        adguard: DNSFilterConfig { enabled: false, last_updated: "Never".into(), filter_devices: list![], device_profiles: list![], ..Default::default() },
        qos: QoSConfig { enabled: false, algorithm: "cake".into(), download_limit_mbps: 100, upload_limit_mbps: 20 },
        accounting: AccountingConfig { enabled: false, retention_months: 13 },
        wifi: WiFiConfig {
            enabled: false,
            interface: "wlan0".into(),
            ssid: "MinimalRouter-Home".into(),
            band: "5ghz".into(),
            channel: 36,
            ..Default::default()
        },
        // Default management trust boundary is the LAN.
        trusted_networks: list!["192.168.1.0/24".to_string()],
        ..Default::default()
    }
}
