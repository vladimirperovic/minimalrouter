export const CONFIG = {
  revision: 42,
  updated_at: new Date().toISOString(),
  system: { hostname: "minimalrouter", domain: "lan", https_enabled: true, https_port: 8443, management_access: "lan_and_wireguard" },
  wan: { interface: "eth0", enabled: true, username: "isp", mtu: 1492 },
  lan: { interface: "eth1", ip_address: "192.168.1.1", netmask: "255.255.255.0", cidr: "192.168.1.1/24" },
  dhcp: { enabled: true, dns_enabled: false, range_start: "192.168.1.100", range_end: "192.168.1.200", lease_time: "12h", dns_servers: ["1.1.1.1"], static_leases: [] },
  dns: { records: [] },
  firewall: { default_wan_input_policy: "deny", wan_ingress_mode: "wireguard_only", stateful_firewall: true, port_forwards: [], custom_rules: [] },
  wireguard: { enabled: true, interface: "wg0", listen_port: 51820, address: "10.8.0.1/24", peers: [] },
  wg_client: { enabled: false, interface: "wg1", address: "", public_key: "", endpoint: "", allowed_ips: [], persistent_keepalive: 25 },
  cloudflare: { ddns_enabled: false, ddns_provider: "noip", domain: "", tunnel_enabled: false },
  squid_proxy: { enabled: false, port: 3128, username: "proxyadmin", restricted_ips: [] },
  adguard: { enabled: false, blocklist_url: "", last_updated: "Never", device_profiles: [] },
  qos: { enabled: false, algorithm: "cake", download_limit_mbps: 100, upload_limit_mbps: 20 },
  accounting: { enabled: false, retention_months: 13 },
  wifi: { enabled: false, interface: "wlan0", ssid: "MinimalRouter-Home", band: "5ghz", channel: 36, hide_ssid: false },
  trusted_networks: ["192.168.1.0/24"],
};
export const SYSTEM = {
  status: "Connected", version: "v0.1", revision: 42, update_trust_configured: true, recovery_required: false,
  runtime: { available: true, wan_connected: true, public_ip: "203.0.113.9", uptime_seconds: 90000, cpu_count: 2,
    cpu_load_percent: 4, memory_used_bytes: 1, memory_total_bytes: 2, disk_used_bytes: 1, disk_total_bytes: 2,
    storage: { available: true, total_bytes: 2, used_bytes: 1, free_bytes: 1, usage_percent: 25, level: "normal",
      nonessential_writes_allowed: true, durable_writes_allowed: true },
    time_synchronized: true, conntrack_count: 1, conntrack_max: 2, dhcp_leases: [], rx_bytes: 1, tx_bytes: 1 },
};
export const HEALTH = { state: "healthy", headline: "ok", checks: [{ id: "dns_dhcp", label: "DNS", state: "healthy", summary: "ok" }], generated_at: new Date().toISOString() };
export const GW_SUMMARY = { available: true, enabled: true, state: "healthy", link: { connected: true, interface: "ppp0" }, packet_loss_percent: 0, pppoe_uptime_seconds: 3600, reconnects_1h: 0, reconnects_24h: 0 };
export const GW_SETTINGS = { enabled: true, targets: ["1.1.1.1"], interval_seconds: 30 };

