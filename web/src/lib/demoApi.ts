export const isDemoMode = import.meta.env.VITE_DEMO_MODE === "true";

const startedAt = Date.now();

let config = {
  revision: 42,
  updated_at: new Date(startedAt).toISOString(),
  system: { hostname: "minimalrouter", domain: "lan", https_enabled: true, https_port: 8443, management_access: "lan_and_wireguard" },
  wan: { interface: "eth0", enabled: true, username: "demo@example.net", password: "[REDACTED]", mtu: 1492 },
  lan: { interface: "eth1", ip_address: "192.168.1.1", netmask: "255.255.255.0", cidr: "192.168.1.1/24" },
  dhcp: {
    enabled: true,
    dns_enabled: true,
    range_start: "192.168.1.100",
    range_end: "192.168.1.200",
    lease_time: "12h",
    dns_servers: ["1.1.1.1", "9.9.9.9"],
    static_leases: [
      { id: "l1", hostname: "studio-mac", mac: "02:4A:71:2C:90:11", ip_address: "192.168.1.20" },
      { id: "l2", hostname: "office-printer", mac: "02:63:8F:14:B2:27", ip_address: "192.168.1.21" },
      { id: "l3", hostname: "living-room-tv", mac: "02:91:3D:6A:C4:38", ip_address: "192.168.1.42" },
      { id: "l4", hostname: "home-nas", mac: "02:B7:50:2E:81:54", ip_address: "192.168.1.30" },
    ],
  },
  dns: { records: [{ name: "nas.lan", ip: "192.168.1.30" }, { name: "printer.lan", ip: "192.168.1.21" }, { name: "media.lan", ip: "192.168.1.42" }] },
  firewall: {
    default_wan_input_policy: "drop",
    wan_ingress_mode: "wireguard_only",
    stateful_firewall: true,
    port_forwards: [{ id: "p1", name: "NAS web", protocol: "tcp", external_port: 18080, internal_ip: "192.168.1.30", internal_port: 80, enabled: true }],
    custom_rules: [
      { id: "c1", name: "Guest printer access", action: "allow", direction: "forward", protocol: "tcp", src_ip: "192.168.1.0/24", dst_port: 9100, enabled: true },
      { id: "c2", name: "Block legacy discovery", action: "deny", direction: "forward", protocol: "udp", src_ip: "192.168.1.0/24", dst_port: 1900, enabled: true },
    ],
  },
  wireguard: {
    enabled: true,
    interface: "wg0",
    listen_port: 51820,
    address: "10.8.0.1/24",
    peers: [
      { id: "w1", name: "MacBook Pro", public_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE=", allowed_ips: ["10.8.0.2/32"], enabled: true },
      { id: "w2", name: "iPhone", public_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAI=", allowed_ips: ["10.8.0.3/32"], enabled: true },
      { id: "w3", name: "Travel laptop", public_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAM=", allowed_ips: ["10.8.0.4/32"], enabled: false },
    ],
  },
  wg_client: { enabled: false, interface: "wg1", address: "", public_key: "", endpoint: "", allowed_ips: [], persistent_keepalive: 25 },
  cloudflare: { ddns_enabled: true, ddns_provider: "noip", ddns_username: "demo", domain: "router.example.com", tunnel_enabled: false },
  squid_proxy: { enabled: false, port: 3128, username: "proxyadmin", restricted_ips: [{ hostname: "studio-mac", ip_address: "192.168.1.20", enabled: true }, { hostname: "home-nas", ip_address: "192.168.1.30", enabled: true }] },
  adguard: {
    enabled: true,
    blocklist_url: "https://example.com/blocklist.txt",
    last_updated: new Date(startedAt - 10_800_000).toISOString(),
    filter_devices: [],
    device_profiles: [
      { id: "k1", name: "Kids tablet", ip_addresses: ["192.168.1.61"], services: ["youtube", "steam"], enabled: true, schedule: { day_windows: { monday: [{ start: "18:00", end: "21:30" }], tuesday: [{ start: "18:00", end: "21:30" }], wednesday: [{ start: "18:00", end: "21:30" }], thursday: [{ start: "18:00", end: "21:30" }], friday: [{ start: "18:00", end: "23:00" }], saturday: [{ start: "09:00", end: "23:00" }], sunday: [{ start: "09:00", end: "21:30" }] } } },
      { id: "k2", name: "Gaming console", ip_addresses: ["192.168.1.64"], services: ["steam", "twitch"], enabled: true, schedule: { day_windows: { monday: [{ start: "17:00", end: "22:00" }], tuesday: [{ start: "17:00", end: "22:00" }], wednesday: [{ start: "17:00", end: "22:00" }], thursday: [{ start: "17:00", end: "22:00" }], friday: [{ start: "17:00", end: "23:59" }], saturday: [{ start: "08:00", end: "23:59" }], sunday: [{ start: "08:00", end: "22:00" }] } } },
    ],
  },
  qos: { enabled: true, algorithm: "cake", download_limit_mbps: 200, upload_limit_mbps: 40 },
  accounting: { enabled: true, retention_months: 13 },
  wifi: { enabled: true, interface: "wlan0", ssid: "MinimalRouter-Home", band: "5ghz", channel: 36, hide_ssid: false },
  trusted_networks: ["192.168.1.0/24"],
};

const leases = [
  { hostname: "studio-mac", ip_address: "192.168.1.20", mac: "02:4A:71:2C:90:11", expires_at: Math.floor(startedAt / 1000) + 38000 },
  { hostname: "office-printer", ip_address: "192.168.1.21", mac: "02:63:8F:14:B2:27", expires_at: Math.floor(startedAt / 1000) + 40100 },
  { hostname: "living-room-tv", ip_address: "192.168.1.42", mac: "02:91:3D:6A:C4:38", expires_at: Math.floor(startedAt / 1000) + 28700 },
  { hostname: "kids-tablet", ip_address: "192.168.1.61", mac: "02:C8:25:73:A0:69", expires_at: Math.floor(startedAt / 1000) + 21400 },
  { hostname: "gaming-console", ip_address: "192.168.1.64", mac: "02:E2:49:16:BD:72", expires_at: Math.floor(startedAt / 1000) + 35600 },
];

const health = {
  state: "warning",
  headline: "1 of 12 checks needs attention.",
  generated_at: new Date(startedAt).toISOString(),
  checks: [
    { id: "wan", label: "WAN", state: "healthy", summary: "PPPoE session is connected" },
    { id: "dns_dhcp", label: "DNS and DHCP", state: "healthy", summary: "Local services are responding" },
    { id: "updates", label: "Updates", state: "healthy", summary: "Firmware verification trust anchor is configured" },
    { id: "backup", label: "Encrypted backup", state: "warning", summary: "Last encrypted backup export was 11 days ago" },
    { id: "firewall", label: "Firewall", state: "healthy", summary: "Default-deny policy is enforced" },
    { id: "wireguard", label: "WireGuard", state: "healthy", summary: "2 remote devices connected" },
    { id: "gateway", label: "Gateway monitoring", state: "healthy", summary: "Both public probes are reachable" },
    { id: "storage", label: "Storage", state: "healthy", summary: "30% used with 5.2 GB free" },
    { id: "time", label: "Time synchronization", state: "healthy", summary: "Clock synchronized" },
    { id: "config", label: "Configuration", state: "healthy", summary: "Canonical revision 42 verified" },
    { id: "ddns", label: "Dynamic DNS", state: "healthy", summary: "router.example.com is in sync" },
    { id: "qos", label: "QoS", state: "healthy", summary: "CAKE is active on ppp0" },
  ],
};

const audit = { events: [
  { id: "6", event_type: "auth.login_succeeded", timestamp: new Date(startedAt - 300000).toISOString(), actor: "192.168.1.20", details: { result: "success" } },
  { id: "5", event_type: "config.applied", timestamp: new Date(startedAt - 3600000).toISOString(), actor: "admin", details: { revision: "42" } },
  { id: "2", event_type: "auth.csrf_rejected", timestamp: new Date(startedAt - 172800000).toISOString(), actor: "192.0.2.77", details: { source: "unknown" } },
  { id: "7", event_type: "wireguard.peer_connected", timestamp: new Date(startedAt - 720000).toISOString(), actor: "10.8.0.2", details: { peer: "MacBook Pro" } },
] };

let snapshots = [
  { id: "s3", created_at: new Date(startedAt - 3_600_000).toISOString(), revision: 42, checksum: "b7f1c3a95d24e08fa1c6d4e2b8093f7a5c1e6d2b4a8f0c3e9d7b5a1f2c4e6d80" },
  { id: "s2", created_at: new Date(startedAt - 86_400_000).toISOString(), revision: 41, checksum: "e3a8d1f60c92b4785ade3c1097f2b6d40e8a5c39b7f1d2e604a8c9b3d5e7f102" },
  { id: "s1", created_at: new Date(startedAt - 604_800_000).toISOString(), revision: 38, checksum: "a2e77f3101d64ea909a7f321d3ec5b9014df22c9d12d5540beae4a942f2518cf" },
];

let trafficTick = 0;
let rxBytes = 91_000_000_000;
let txBytes = 12_400_000_000;

function liveSystem() {
  const downloadMbps = [18.6, 24.3, 31.8, 27.1, 42.7, 36.4, 21.9, 29.6][trafficTick % 8];
  const uploadMbps = [3.8, 5.1, 4.4, 7.2, 6.3, 4.9, 8.1, 5.7][trafficTick % 8];
  rxBytes += Math.round(downloadMbps * 1024 * 1024 * 2 / 8);
  txBytes += Math.round(uploadMbps * 1024 * 1024 * 2 / 8);
  trafficTick += 1;
  return {
    status: "Connected",
    version: "v0.1.6-demo",
    revision: config.revision,
    update_trust_configured: true,
    recovery_required: false,
    runtime: {
      available: true, wan_connected: true, public_ip: "203.0.113.9", wan_mac: "02:D4:7A:31:8C:10", lan_mac: "02:6B:92:4F:E1:20",
      uptime_seconds: 691200 + Math.floor((Date.now() - startedAt) / 1000), cpu_count: 2, cpu_load_percent: 6 + trafficTick % 4,
      load_average: [0.14, 0.11, 0.09], memory_used_bytes: 151000000, memory_total_bytes: 536870912, app_memory_bytes: 96000000,
      disk_used_bytes: 2400000000, disk_total_bytes: 8000000000,
      storage: { available: true, total_bytes: 8000000000, used_bytes: 2400000000, free_bytes: 5600000000, usage_percent: 30, level: "normal", nonessential_writes_allowed: true, durable_writes_allowed: true },
      time_synchronized: true, conntrack_count: 1284, conntrack_max: 131072, conntrack_usage_percent: 1,
      rx_bytes: rxBytes, tx_bytes: txBytes, wireguard_active_peers: 2, dhcp_leases: leases,
      wireguard_peers: [
        { public_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE=", endpoint: "198.51.100.24:51820", allowed_ips: "10.8.0.2/32", last_handshake_epoch: Math.floor(Date.now() / 1000) - 45, rx_bytes: 48100000, tx_bytes: 129800000, online: true },
        { public_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAI=", endpoint: "192.0.2.88:59214", allowed_ips: "10.8.0.3/32", last_handshake_epoch: Math.floor(Date.now() / 1000) - 118, rx_bytes: 17600000, tx_bytes: 62400000, online: true },
      ],
      ddns: { running: true, last_ip: "203.0.113.9", last_update_epoch: Math.floor(Date.now() / 1000) - 1800, hostname: "router.example.com" },
    },
  };
}

function gatewayHistory() {
  return Array.from({ length: 110 }, (_, index) => ({
    timestamp: new Date(Date.now() - (110 - index) * 30000).toISOString(), state: "healthy",
    latency_ms: 18.2 + Math.sin(index / 9) * 0.42 + Math.sin(index / 2.7) * 0.11,
    jitter_ms: 1.8 + Math.sin(index / 11) * 0.18, packet_loss_percent: 0,
    pppoe_uptime_seconds: 691200 - (110 - index) * 30,
    rx_bytes: 900000000 + index * 2100000, tx_bytes: 120000000 + index * 310000,
  }));
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store" } });
}

function download(body: string, type: string) {
  return new Response(body, { status: 200, headers: { "Content-Type": type, "Cache-Control": "no-store" } });
}

function parseBody(init: RequestInit) {
  if (typeof init.body !== "string") return null;
  try { return JSON.parse(init.body) as Record<string, unknown>; } catch { return null; }
}

export async function demoApiFetch(input: RequestInfo | URL, init: RequestInit = {}) {
  const raw = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
  const url = new URL(raw, window.location.origin);
  const path = url.pathname;
  const method = (init.method ?? "GET").toUpperCase();

  if (path === "/api/v1/config" && method === "PUT") {
    const next = parseBody(init);
    if (next) config = { ...config, ...next, revision: config.revision + 1, updated_at: new Date().toISOString() } as typeof config;
    return json({ state: "Committed", revision: config.revision });
  }
  if (path === "/api/v1/gateway/settings" && method === "PUT") return json(parseBody(init) ?? {});
  if (path === "/api/v1/qos/speedtest" && method === "POST") return json({ download_mbps: 221.4, upload_mbps: 46.8, suggested_download_mbps: 199, suggested_upload_mbps: 42 });
  if (path === "/api/v1/snapshots" && method === "POST") {
    snapshots = [{ id: `demo-${Date.now()}`, created_at: new Date().toISOString(), revision: config.revision, checksum: "demo000000000000000000000000000000000000000000000000000000000000" }, ...snapshots];
    return json({ created: true });
  }
  if (path === "/api/v1/wireguard/client/keys" && method === "POST") return json({ private_key: "DEMO_PRIVATE_KEY_NOT_REAL=", public_key: "DEMO_PUBLIC_KEY_NOT_REAL=" });
  if (path === "/api/v1/wireguard/peers" && method === "POST") {
    const peer = { id: `demo-${Date.now()}`, name: String(parseBody(init)?.name ?? "Demo device"), public_key: "DEMO_PUBLIC_KEY_NOT_REAL=", allowed_ips: ["10.8.0.5/32"], enabled: true };
    config.wireguard.peers = [...config.wireguard.peers, peer];
    config.revision += 1;
    return json({ peer, client_config: "# Demo-only WireGuard configuration\n[Interface]\nAddress = 10.8.0.5/32", qr_code_data: "", tx: { state: "Committed" } });
  }
  const peerConfigurationMatch = path.match(/^\/api\/v1\/wireguard\/peers\/([^/]+)\/configuration$/);
  if (peerConfigurationMatch && method === "POST") {
    const peer = config.wireguard.peers.find((item) => item.id === decodeURIComponent(peerConfigurationMatch[1]));
    if (!peer) return json({ error: "WireGuard peer not found" }, 404);
    return json({ peer, client_config: `# Demo-only regenerated WireGuard configuration\n[Interface]\nAddress = ${peer.allowed_ips[0]}`, qr_code_data: "", tx: { state: "Committed" } });
  }
  const peerDeleteMatch = path.match(/^\/api\/v1\/wireguard\/peers\/([^/]+)$/);
  if (peerDeleteMatch && method === "DELETE") {
    config.wireguard.peers = config.wireguard.peers.filter((item) => item.id !== decodeURIComponent(peerDeleteMatch[1]));
    config.revision += 1;
    return json({ tx: { state: "Committed" } });
  }
  if (path === "/api/v1/auth/totp/enroll" && method === "POST") return json({ secret: "DEMOONLYSECRET", qr_uri: "otpauth://totp/MinimalRouter:demo?secret=DEMOONLYSECRET&issuer=MinimalRouter" });
  if (path === "/api/v1/backup/import/preview" && method === "POST") return json({ import_id: "demo-backup", expires_in_seconds: 600, candidate: config });
  if (path === "/api/v1/import/pfsense/preview" && method === "POST") return json({ import_id: "demo-pfsense", expires_in_seconds: 600, report: { source_version: "2.7-demo", warnings: [], unsupported_sections: [], imported: { dhcp_leases: 4, firewall_rules: 2 }, config } });
  if (path === "/api/v1/system/diagnostics" && method === "GET") return download(JSON.stringify({ mode: "public-demo", secrets: "redacted", revision: config.revision }, null, 2), "application/json");
  if (path === "/api/v1/backup/export" && method === "POST") return download("MINIMALROUTER PUBLIC DEMO BACKUP\nNo router data is included.\n", "application/octet-stream");
  if (method !== "GET") return json({ state: "Committed", demo: true });

  if (path === "/api/v1/auth/session") return json({ authenticated: true, csrf_token: "public-demo", read_only: false, totp_enabled: false });
  if (path === "/api/v1/setup/status") return json({ is_configured: true });
  if (path === "/api/v1/config") return json(config);
  if (path === "/api/v1/system") return json(liveSystem());
  if (path === "/api/v1/health") return json(health);
  if (path === "/api/v1/gateway/summary") return json({ available: true, enabled: true, state: "healthy", link: { connected: true, interface: "ppp0", local_ip: "203.0.113.9", peer_ip: "203.0.113.1" }, latency_ms: 18.4, jitter_ms: 2.1, packet_loss_percent: 0, pppoe_uptime_seconds: 691200, reconnects_1h: 0, reconnects_24h: 0, targets: ["1.1.1.1", "9.9.9.9"] });
  if (path === "/api/v1/gateway/settings") return json({ enabled: true, targets: ["1.1.1.1", "9.9.9.9"], interval_seconds: 30 });
  if (path === "/api/v1/gateway/history") return json({ window: url.searchParams.get("window") ?? "1h", points: gatewayHistory() });
  if (path === "/api/v1/snapshots") return json({ snapshots });
  if (path === "/api/v1/transactions/pending") return json({});
  if (path === "/api/v1/audit/events") return json(audit);
  if (path === "/api/v1/accounting") return json({ available: true, enabled: true, updated_at: new Date().toISOString(), months: [{ month: new Date().toISOString().slice(0, 7), total_bytes: 143684000000, devices: [
    { address: "192.168.1.20", hostname: "studio-mac", mac: "02:4A:71:2C:90:11", rx_bytes: 61420000000, tx_bytes: 8310000000, total_bytes: 69730000000, last_seen_epoch: Math.floor(Date.now() / 1000) - 42 },
    { address: "192.168.1.42", hostname: "living-room-tv", mac: "02:91:3D:6A:C4:38", rx_bytes: 43170000000, tx_bytes: 1870000000, total_bytes: 45040000000, last_seen_epoch: Math.floor(Date.now() / 1000) - 110 },
    { address: "192.168.1.64", hostname: "gaming-console", mac: "02:E2:49:16:BD:72", rx_bytes: 24650000000, tx_bytes: 3120000000, total_bytes: 27770000000, last_seen_epoch: Math.floor(Date.now() / 1000) - 360 },
  ] }] });
  if (path === "/api/v1/wireguard/provisioning-preview") return json({ next_address: "10.8.0.5/32", client_ip: "10.8.0.5/32", server_endpoint: "router.example.com:51820", listen_port: 51820 });
  return json({});
}
