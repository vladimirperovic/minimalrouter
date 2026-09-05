import { expect, test, type Page, type Route } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";

/*
 * Produces the screenshots published in docs/screenshots/.
 *
 * Every value below is synthetic and safe to publish: addresses come from the
 * ranges reserved for documentation (RFC 5737 192.0.2.0/24, 198.51.100.0/24 and
 * 203.0.113.0/24), MAC addresses from the RFC 7042 documentation range
 * 00:00:5E:00:53:00-FF, and domains from RFC 2606 example.* names. The LAN block
 * is the product's own default, not anyone's network. No real appliance is ever
 * contacted -- the whole API is stubbed here.
 *
 * Run with:  pnpm exec playwright test --project=chromium e2e/screenshots.spec.ts
 */

const OUT = resolve(process.cwd(), "..", "docs", "screenshots");
mkdirSync(OUT, { recursive: true });

const NOW = new Date("2026-03-14T09:41:00Z");

const CONFIG = {
  revision: 42,
  updated_at: NOW.toISOString(),
  system: { hostname: "minimalrouter", domain: "lan", https_enabled: true, https_port: 8443, management_access: "lan_and_wireguard" },
  wan: { interface: "eth0", enabled: true, username: "user@example.net", password: "[REDACTED]", mtu: 1492 },
  lan: { interface: "eth1", ip_address: "192.168.1.1", netmask: "255.255.255.0", cidr: "192.168.1.1/24" },
  dhcp: {
    enabled: true, dns_enabled: true, range_start: "192.168.1.100", range_end: "192.168.1.200",
    lease_time: "12h", dns_servers: ["1.1.1.1", "9.9.9.9"],
    static_leases: [
      { id: "l1", hostname: "workstation", mac: "00:00:5e:00:53:11", ip_address: "192.168.1.20" },
      { id: "l2", hostname: "printer", mac: "00:00:5e:00:53:12", ip_address: "192.168.1.21" },
    ],
  },
  dns: { records: [{ name: "nas.lan", ip: "192.168.1.30" }] },
  firewall: {
    default_wan_input_policy: "deny", wan_ingress_mode: "wireguard_only", stateful_firewall: true,
    port_forwards: [
      { id: "p1", name: "NAS web", protocol: "tcp", external_port: 18080, internal_ip: "192.168.1.30", internal_port: 80, enabled: true },
    ],
    custom_rules: [
      { id: "c1", name: "Guest to printer", direction: "forward", action: "allow", protocol: "tcp", src_ip: "192.168.1.0/24", dst_port: 9100, enabled: true },
    ],
  },
  wireguard: {
    enabled: true, interface: "wg0", listen_port: 51820, address: "10.8.0.1/24",
    peers: [
      { id: "w1", name: "laptop", public_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE=", allowed_ips: ["10.8.0.2/32"], enabled: true },
      { id: "w2", name: "phone", public_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAI=", allowed_ips: ["10.8.0.3/32"], enabled: true },
    ],
  },
  wg_client: { enabled: false, interface: "wg1", address: "", public_key: "", endpoint: "", allowed_ips: [], persistent_keepalive: 25 },
  cloudflare: { ddns_enabled: true, ddns_provider: "noip", domain: "router.example.com", ddns_username: "user@example.net", tunnel_enabled: false },
  squid_proxy: { enabled: false, port: 3128, username: "proxyadmin", restricted_ips: [] },
  adguard: {
    enabled: true, blocklist_url: "https://example.com/blocklist.txt", last_updated: "2026-03-14 06:00",
    device_profiles: [
      { id: "k1", name: "Kids tablet", ip_addresses: ["192.168.1.60"], services: ["youtube", "steam"], enabled: true,
        schedule: { day_windows: {
          monday: [{ start: "19:00", end: "23:59" }], tuesday: [{ start: "19:00", end: "23:59" }],
          wednesday: [{ start: "19:00", end: "23:59" }], thursday: [{ start: "19:00", end: "23:59" }],
          friday: [{ start: "19:00", end: "23:59" }], saturday: [{ start: "00:00", end: "23:59" }],
          sunday: [{ start: "00:00", end: "23:59" }],
        } } },
    ],
  },
  qos: { enabled: true, algorithm: "cake", download_limit_mbps: 200, upload_limit_mbps: 40 },
  accounting: { enabled: true, retention_months: 13 },
  wifi: { enabled: true, interface: "wlan0", ssid: "MinimalRouter-Home", band: "5ghz", channel: 36, hide_ssid: false, passphrase: "[REDACTED]" },
  trusted_networks: ["192.168.1.0/24"],
};

const SYSTEM = {
  status: "Connected", version: "v0.1-alpha", revision: 42, update_trust_configured: true, recovery_required: false,
  runtime: {
    available: true, wan_connected: true, public_ip: "203.0.113.9", uptime_seconds: 691_200,
    cpu_count: 2, cpu_load_percent: 7, load_average: [0.14, 0.11, 0.09],
    memory_used_bytes: 214_000_000, memory_total_bytes: 1_024_000_000,
    disk_used_bytes: 2_400_000_000, disk_total_bytes: 8_000_000_000,
    wan_mac: "00:00:5e:00:53:01", lan_mac: "00:00:5e:00:53:02",
    storage: { available: true, total_bytes: 8_000_000_000, used_bytes: 2_400_000_000, free_bytes: 5_600_000_000, usage_percent: 30, level: "normal", nonessential_writes_allowed: true, durable_writes_allowed: true },
    time_synchronized: true, conntrack_count: 1_284, conntrack_max: 131_072, conntrack_usage_percent: 1,
    rx_bytes: 91_000_000_000, tx_bytes: 12_400_000_000,
    wireguard_active_peers: 1,
    wireguard_peers: [
      { public_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE=", endpoint: "198.51.100.24:51820", allowed_ips: "10.8.0.2/32", last_handshake_epoch: Math.floor(NOW.getTime() / 1000) - 45, rx_bytes: 4_100_000, tx_bytes: 9_800_000, online: true },
      { public_key: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAI=", endpoint: "", allowed_ips: "10.8.0.3/32", last_handshake_epoch: 0, rx_bytes: 0, tx_bytes: 0, online: false },
    ],
    ddns: { running: true, last_ip: "203.0.113.9", last_update_epoch: Math.floor(NOW.getTime() / 1000) - 1800, hostname: "router.example.com" },
    dhcp_leases: [
      { hostname: "workstation", ip_address: "192.168.1.20", mac: "00:00:5e:00:53:11", expires_at: Math.floor(NOW.getTime() / 1000) + 38_000 },
      { hostname: "kids-tablet", ip_address: "192.168.1.60", mac: "00:00:5e:00:53:13", expires_at: Math.floor(NOW.getTime() / 1000) + 21_500 },
      { hostname: "printer", ip_address: "192.168.1.21", mac: "00:00:5e:00:53:12", expires_at: Math.floor(NOW.getTime() / 1000) + 40_100 },
    ],
  },
};

const HEALTH = {
  state: "healthy", headline: "All appliance checks are passing.", generated_at: NOW.toISOString(),
  checks: [
    { id: "dns_dhcp", label: "DNS and DHCP", state: "healthy", summary: "dnsmasq is serving 3 leases" },
    { id: "wan", label: "WAN", state: "healthy", summary: "PPPoE session up for 8 days" },
    { id: "wireguard", label: "WireGuard", state: "healthy", summary: "1 of 2 peers connected" },
    { id: "storage", label: "Storage", state: "healthy", summary: "30% used" },
    { id: "transaction", label: "Configuration state", state: "healthy", summary: "Canonical revision 42 verified" },
    { id: "time", label: "Time synchronisation", state: "healthy", summary: "Clock synchronised" },
  ],
};

function historyPoints(count: number) {
  const points = [];
  for (let i = count; i > 0; i -= 1) {
    const jitter = Math.sin(i / 3) * 2.2;
    points.push({
      timestamp: new Date(NOW.getTime() - i * 30_000).toISOString(),
      state: "healthy",
      latency_ms: i % 37 === 0 ? undefined : Math.round((18 + jitter + (i % 5)) * 10) / 10,
      jitter_ms: Math.round((2 + Math.abs(jitter)) * 10) / 10,
      packet_loss_percent: i % 53 === 0 ? 2 : 0,
      pppoe_uptime_seconds: 691_200 - i * 30,
      rx_bytes: 900_000_000 + i * 2_100_000,
      tx_bytes: 120_000_000 + i * 310_000,
    });
  }
  return points;
}

const ACCOUNTING = {
  available: true, enabled: true, updated_at: NOW.toISOString(),
  months: [
    { month: "2026-03", total_bytes: 64_234_000_000, devices: [
      { address: "192.168.1.20", hostname: "workstation", mac: "00:00:5e:00:53:11", rx_bytes: 41_200_000_000, tx_bytes: 3_100_000_000, total_bytes: 44_300_000_000, last_seen_epoch: Math.floor(NOW.getTime() / 1000) - 120 },
      { address: "192.168.1.60", hostname: "kids-tablet", mac: "00:00:5e:00:53:13", rx_bytes: 18_900_000_000, tx_bytes: 900_000_000, total_bytes: 19_800_000_000, last_seen_epoch: Math.floor(NOW.getTime() / 1000) - 900 },
      { address: "192.168.1.21", hostname: "printer", mac: "00:00:5e:00:53:12", rx_bytes: 120_000_000, tx_bytes: 14_000_000, total_bytes: 134_000_000, last_seen_epoch: Math.floor(NOW.getTime() / 1000) - 7_200 },
    ] },
    { month: "2026-02", total_bytes: 128_200_000_000, devices: [
      { address: "192.168.1.20", hostname: "workstation", mac: "00:00:5e:00:53:11", rx_bytes: 88_400_000_000, tx_bytes: 6_900_000_000, total_bytes: 95_300_000_000 },
      { address: "192.168.1.60", hostname: "kids-tablet", mac: "00:00:5e:00:53:13", rx_bytes: 31_500_000_000, tx_bytes: 1_400_000_000, total_bytes: 32_900_000_000 },
    ] },
  ],
};

const AUDIT = {
  events: [
    { id: "6", event_type: "auth.login_succeeded", timestamp: new Date(NOW.getTime() - 300_000).toISOString(), actor: "admin", details: { source: "192.168.1.20" } },
    { id: "5", event_type: "config.applied", timestamp: new Date(NOW.getTime() - 3_600_000).toISOString(), actor: "admin", details: { revision: "42" } },
    { id: "4", event_type: "config.confirmed", timestamp: new Date(NOW.getTime() - 3_500_000).toISOString(), actor: "admin", details: { revision: "42" } },
    { id: "3", event_type: "snapshot.created", timestamp: new Date(NOW.getTime() - 86_400_000).toISOString(), actor: "system", details: { revision: "41" } },
    { id: "2", event_type: "auth.csrf_rejected", timestamp: new Date(NOW.getTime() - 172_800_000).toISOString(), actor: "unknown", details: { source: "192.0.2.77" } },
    { id: "1", event_type: "auth.login_succeeded", timestamp: new Date(NOW.getTime() - 259_200_000).toISOString(), actor: "admin", details: { source: "192.168.1.20" } },
  ],
};

const SNAPSHOTS = [
  { id: "s2", created_at: new Date(NOW.getTime() - 3_600_000).toISOString(), revision: 42, checksum: "b7f1c3a95d24e08fa1c6d4e2b8093f7a5c1e6d2b4a8f0c3e9d7b5a1f2c4e6d80" },
  { id: "s1", created_at: new Date(NOW.getTime() - 86_400_000).toISOString(), revision: 41, checksum: "e3a8d1f60c92b4785ade3c1097f2b6d40e8a5c39b7f1d2e604a8c9b3d5e7f102" },
];

async function stub(page: Page) {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

  await page.route("**/api/v1/auth/session", (r) => json(r, { authenticated: true, csrf_token: "screenshot", read_only: false, totp_enabled: false }));
  await page.route("**/api/v1/setup/status", (r) => json(r, { is_configured: true }));
  await page.route("**/api/v1/config", (r) => json(r, CONFIG));
  let tick = 0;
  await page.route("**/api/v1/system", (r) => {
    tick += 1;
    const rx = SYSTEM.runtime.rx_bytes + tick * 5_600_000;
    const tx = SYSTEM.runtime.tx_bytes + tick * 940_000;
    return json(r, { ...SYSTEM, runtime: { ...SYSTEM.runtime, rx_bytes: rx, tx_bytes: tx } });
  });
  await page.route("**/api/v1/health", (r) => json(r, HEALTH));
  await page.route("**/api/v1/gateway/summary", (r) => json(r, {
    available: true, enabled: true, state: "healthy",
    link: { connected: true, interface: "ppp0", local_ip: "203.0.113.9", peer_ip: "203.0.113.1" },
    latency_ms: 18.4, jitter_ms: 2.1, packet_loss_percent: 0,
    pppoe_uptime_seconds: 691_200, reconnects_1h: 0, reconnects_24h: 0,
    targets: [
      { target: "1.1.1.1", reachable: true, packets_sent: 10, packets_received: 10, packet_loss_percent: 0, latency_ms: 18.4, jitter_ms: 2.1 },
      { target: "9.9.9.9", reachable: true, packets_sent: 10, packets_received: 10, packet_loss_percent: 0, latency_ms: 19.1, jitter_ms: 2.4 },
    ],
  }));
  await page.route("**/api/v1/gateway/settings", (r) => json(r, { enabled: true, targets: ["1.1.1.1", "9.9.9.9"], interval_seconds: 30 }));
  await page.route("**/api/v1/gateway/history**", (r) => json(r, { window: "1h", points: historyPoints(110) }));
  await page.route("**/api/v1/snapshots", (r) => json(r, { snapshots: SNAPSHOTS }));
  await page.route("**/api/v1/transactions/pending", (r) => json(r, {}));
  await page.route("**/api/v1/audit/events**", (r) => json(r, AUDIT));
  await page.route("**/api/v1/accounting**", (r) => json(r, ACCOUNTING));
  await page.route("**/api/v1/wireguard/provisioning-preview**", (r) => json(r, { client_ip: "10.8.0.4/32", server_endpoint: "router.example.com:51820", ddns_configured: true, wireguard_enabled: true }));
  // No catch-all: Playwright runs the most recently registered matching handler,
  // so a broad pattern here would shadow every specific stub above it. Anything
  // unstubbed simply fails, which the dashboard already degrades gracefully.
}

const SECTIONS: Array<[string, string]> = [
  ["overview", "01-overview"],
  ["gateway", "02-gateway-quality"],
  ["network", "03-lan-and-dhcp"],
  ["firewall", "04-firewall"],
  ["security", "05-security"],
  ["dns-filter", "06-dns-filter"],
  ["qos", "07-qos-sqm"],
  ["wireguard", "08-wireguard"],
  ["cloudflare", "09-dynamic-dns"],
  ["wifi", "10-wifi-ap"],
  ["traffic", "11-traffic"],
  ["squid", "12-squid-proxy"],
  ["recovery", "13-recovery"],
  ["logs", "14-logs"],
];

// A tall viewport with fullPage disabled. The sidebar and top bar are fixed, and
// a fullPage capture re-renders fixed elements at their scroll offset, which
// scatters them through the middle of the image.
test.use({ viewport: { width: 1440, height: 1800 } });

// A capture utility, not a behavioural test. One deterministic engine is enough,
// and the other projects would only produce duplicate images.
test.skip(
  ({ browserName, isMobile }) => browserName !== "chromium" || isMobile,
  "screenshots are captured on desktop chromium only",
);

test("capture every dashboard section", async ({ page }) => {
  const crashes: string[] = [];
  page.on("pageerror", (e) => crashes.push(e.message));
  await stub(page);
  await page.goto("/");
  // The live bandwidth chart needs a few polls before it has anything to draw.
  await page.waitForTimeout(6_000);

  for (const [id, file] of SECTIONS) {
    const link = page.locator(`a[href="#${id}"]`).first();
    await link.waitFor({ state: "visible" });
    await link.click();
    await page.waitForFunction(
      (section) => document.querySelector(`a[href="#${section}"]`)?.classList.contains("is-active") === true,
      id,
    );
    await page.waitForTimeout(700);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.waitForTimeout(150);
    await page.screenshot({ path: resolve(OUT, `${file}.png`) });
  }
  // A screenshot of a crashed page is worse than no screenshot.
  expect(crashes, "the dashboard threw while capturing").toEqual([]);
});

test("capture the dark theme overview", async ({ page }) => {
  await stub(page);
  await page.goto("/");
  await page.waitForTimeout(700);
  const toggle = page.getByRole("button", { name: /toggle appearance/i }).first();
  if (await toggle.count()) {
    await toggle.click();
    await page.waitForTimeout(500);
    await page.screenshot({ path: resolve(OUT, "15-overview-dark.png") });
  }
});

test("capture the setup wizard", async ({ page }) => {
  const DISCOVERY = {
    wan: "eth0", lan: "eth1", warnings: [],
    interfaces: [
      { name: "eth0", mac_address: "00:00:5e:00:53:01", up: true, carrier: true, physical: true, default_route: true, score: 215 },
      { name: "eth1", mac_address: "00:00:5e:00:53:02", up: true, carrier: true, physical: true, default_route: false, score: 135 },
    ],
  };
  await page.route("**/api/v1/auth/session", (r) => r.fulfill({ status: 401, contentType: "application/json", body: "{}" }));
  await page.route("**/api/v1/setup/status", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ is_configured: false, wan_interface: "eth0", lan_interface: "eth1" }) }));
  await page.route("**/api/v1/setup/interfaces", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(DISCOVERY) }));

  await page.goto("/");
  await page.waitForTimeout(500);
  await page.screenshot({ path: resolve(OUT, "20-setup-welcome.png") });

  await page.getByRole("button", { name: /Start setup/ }).click();
  await page.waitForTimeout(350);
  await page.screenshot({ path: resolve(OUT, "21-setup-interfaces.png") });

  await page.getByRole("button", { name: /Continue/ }).click();
  await page.waitForTimeout(300);
  await page.screenshot({ path: resolve(OUT, "22-setup-pppoe.png") });

  await page.getByRole("button", { name: /Continue/ }).click();
  await page.waitForTimeout(300);
  const pw = page.locator('input[type="password"]');
  await pw.nth(0).fill("correct-horse-battery-staple");
  await pw.nth(1).fill("correct-horse-battery-staple");
  await page.waitForTimeout(200);
  await page.screenshot({ path: resolve(OUT, "23-setup-password.png") });

  await page.getByRole("button", { name: /Review/ }).click();
  await page.waitForTimeout(300);
  await page.screenshot({ path: resolve(OUT, "24-setup-review.png") });
});

test("capture the mobile overview", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await stub(page);
  await page.goto("/");
  await page.waitForTimeout(900);
  await page.screenshot({ path: resolve(OUT, "30-overview-mobile.png"), fullPage: true });
});
