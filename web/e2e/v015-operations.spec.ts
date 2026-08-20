import { expect, test, type Page, type Route } from "@playwright/test";

const NOW = new Date("2026-08-18T15:14:00Z");
const CURRENT_EPOCH = Math.floor(Date.now() / 1000);

const CONFIG = {
  revision: 42,
  updated_at: NOW.toISOString(),
  system: { hostname: "minimalrouter", domain: "lan", https_enabled: true, https_port: 8443, management_access: "lan_and_wireguard" },
  wan: { interface: "eth0", enabled: true, username: "isp", mtu: 1492 },
  lan: { interface: "eth1", ip_address: "192.168.1.1", netmask: "255.255.255.0", cidr: "192.168.1.1/24" },
  dhcp: { enabled: true, dns_enabled: true, range_start: "192.168.1.100", range_end: "192.168.1.200", lease_time: "12h", dns_servers: ["1.1.1.1"], static_leases: [] },
  dns: { records: [] },
  firewall: { default_wan_input_policy: "deny", wan_ingress_mode: "wireguard_only", stateful_firewall: true, port_forwards: [], custom_rules: [], extra_lans: [] },
  wireguard: { enabled: true, interface: "wg0", listen_port: 51820, address: "10.8.0.1/24", peers: [] },
  wg_client: { enabled: false, interface: "wg1", address: "", public_key: "", endpoint: "", allowed_ips: [], persistent_keepalive: 25 },
  cloudflare: { ddns_enabled: true, ddns_provider: "noip", domain: "router.example.net", tunnel_enabled: false },
  squid_proxy: { enabled: false, port: 3128, username: "proxyadmin", restricted_ips: [] },
  adguard: { enabled: false, blocklist_url: "", last_updated: "Never", device_profiles: [] },
  qos: { enabled: false, algorithm: "cake", download_limit_mbps: 100, upload_limit_mbps: 20 },
  accounting: { enabled: true, retention_months: 13 },
  wifi: { enabled: false, interface: "wlan0", ssid: "MinimalRouter-Home", band: "5ghz", channel: 36, hide_ssid: false },
  trusted_networks: ["192.168.1.0/24"],
};

const SYSTEM = {
  status: "Connected",
  revision: 42,
  runtime: {
    available: true,
    wan_connected: true,
    public_ip: "203.0.113.25",
    uptime_seconds: 90000,
    cpu_count: 2,
    cpu_load_percent: 4,
    memory_used_bytes: 180_000_000,
    memory_total_bytes: 1_000_000_000,
    disk_used_bytes: 2_000_000_000,
    disk_total_bytes: 8_000_000_000,
    time_synchronized: true,
    conntrack_count: 120,
    conntrack_max: 131072,
    rx_bytes: 1000,
    tx_bytes: 500,
    dhcp_leases: [
      { hostname: "Kids iPad", ip_address: "192.168.1.60", mac: "00:00:5e:00:53:13", expires_at: CURRENT_EPOCH + 21_500 },
    ],
  },
};

const ACCOUNTING = {
  available: true,
  enabled: true,
  updated_at: NOW.toISOString(),
  months: [
    { month: "2026-08", total_bytes: 19_800_000_000, devices: [
      { address: "192.168.1.60", hostname: "Kids iPad", mac: "00:00:5e:00:53:13", rx_bytes: 18_900_000_000, tx_bytes: 900_000_000, total_bytes: 19_800_000_000, last_seen_epoch: CURRENT_EPOCH - 60 },
    ] },
    { month: "2026-07", total_bytes: 1_000, devices: [] },
  ],
};

async function stubDashboard(page: Page) {
  const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  await page.route("**/api/v1/auth/session", (route) => json(route, { authenticated: true, csrf_token: "test" }));
  await page.route("**/api/v1/setup/status", (route) => json(route, { first_run: false, is_configured: true }));
  await page.route("**/api/v1/config", (route) => json(route, CONFIG));
  await page.route("**/api/v1/system", (route) => json(route, SYSTEM));
  await page.route("**/api/v1/gateway/summary", (route) => json(route, { available: true, enabled: true, state: "healthy", link: { connected: true, interface: "ppp0", local_ip: "203.0.113.25" }, latency_ms: 18.4, jitter_ms: 2.1, packet_loss_percent: 0, pppoe_uptime_seconds: 86_400, reconnects_1h: 0, reconnects_24h: 0 }));
  await page.route("**/api/v1/gateway/settings", (route) => json(route, { enabled: true, targets: ["1.1.1.1", "8.8.8.8"], interval_seconds: 30 }));
  await page.route("**/api/v1/gateway/history**", (route) => json(route, { window: "1h", points: [] }));
  await page.route("**/api/v1/gateway/insights", (route) => json(route, {
    window_days: 30,
    available: true,
    sampled_hours: 720,
    samples: 86_400,
    up_samples: 86_374,
    uptime_percent: 99.9699,
    outages: 3,
    public_ip_changes: [{ timestamp: "2026-08-18T03:14:00Z", old_ip: "77.46.245.108", new_ip: "203.0.113.25" }],
  }));
  await page.route("**/api/v1/snapshots", (route) => json(route, []));
  await page.route("**/api/v1/transactions/pending", (route) => json(route, {}));
  await page.route("**/api/v1/health", (route) => json(route, { state: "healthy", headline: "healthy", checks: [], generated_at: NOW.toISOString() }));
  await page.route("**/api/v1/audit/events**", (route) => json(route, { events: [] }));
  await page.route("**/api/v1/accounting**", (route) => json(route, ACCOUNTING));
  await page.route("**/api/v1/devices/pauses", (route) => json(route, { pauses: [] }));
}

async function openSection(page: Page, isMobile: boolean | undefined, name: string) {
  if (isMobile) await page.getByRole("button", { name: "Open navigation" }).click();
  await page.getByRole("link", { name }).click();
}

test("Gateway Health exposes measured availability, IP changes and fixed recovery actions", async ({ page, isMobile }) => {
  await stubDashboard(page);
  let requestedAction = "";
  await page.route("**/api/v1/system/actions/*", async (route) => {
    requestedAction = new URL(route.request().url()).pathname.split("/").pop() || "";
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ success: true, action: requestedAction }) });
  });

  await page.goto("/");
  await openSection(page, isMobile, "Gateway Quality");
  await expect(page.getByText("99.97%", { exact: true })).toBeVisible();
  await expect(page.getByText(/30 days · 3 outages/)).toBeVisible();
  const ipHistory = page.locator(".gateway-ip-history");
  await expect(ipHistory.getByRole("heading", { name: "Public IP history" })).toBeVisible();
  await expect(ipHistory.getByText("77.46.245.108", { exact: true })).toBeVisible();
  await expect(ipHistory.getByText("203.0.113.25", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Reconnect WAN" }).click();
  await expect.poll(() => requestedAction).toBe("wan-reconnect");
  await expect(page.getByText("WAN reconnect completed.")).toBeVisible();
});

test("Connected devices shows activity and sends a timed Internet pause", async ({ page, isMobile }) => {
  await stubDashboard(page);
  let pauseRequest: { ip?: string; seconds?: number } = {};
  await page.route("**/api/v1/devices/pause", async (route) => {
    pauseRequest = route.request().postDataJSON() as { ip?: string; seconds?: number };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ success: true, pauses: [{ ip: "192.168.1.60", until_unix: Math.floor(Date.now() / 1000) + 900 }] }) });
  });

  await page.goto("/");
  await openSection(page, isMobile, "LAN & DHCP");
  const row = page.getByRole("row", { name: /Kids iPad/ });
  await expect(row).toContainText("Online");
  await expect(row).toContainText("New");
  await row.getByRole("button", { name: "Pause Internet" }).click();
  await page.getByRole("button", { name: "15 min" }).click();
  await expect.poll(() => pauseRequest).toEqual({ ip: "192.168.1.60", seconds: 900 });
  await expect(row).toContainText("Paused");
  await expect(row.getByRole("button", { name: "Resume" })).toBeVisible();
});
