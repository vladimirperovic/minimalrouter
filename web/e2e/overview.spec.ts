import { expect, test, type Page, type Route } from "@playwright/test";

/*
 * The dashboard had exactly one end-to-end test (the DNS Filter scheduler) and
 * three unit tests for a pure helper. Nothing exercised the Overview, which is
 * where every "always green" defect lived: a hardcoded DNS chip, a resource note
 * that read "within normal operating range" at 99% disk, a notification bell
 * that always reported no alerts, and an appliance-health API the UI never
 * called at all.
 *
 * These tests stub the API so each check drives one specific rendering decision.
 * They assert the honest-reporting rules from DESIGN.md: missing data is shown
 * as unavailable, never simulated, and degraded state is never shown as healthy.
 */

const CONFIG = {
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

function systemStatus(overrides: Record<string, unknown> = {}) {
  return {
    status: "Connected",
    version: "v0.1-alpha",
    revision: 42,
    update_trust_configured: true,
    recovery_required: false,
    runtime: {
      available: true,
      wan_connected: true,
      public_ip: "203.0.113.9",
      uptime_seconds: 90000,
      cpu_count: 2,
      cpu_load_percent: 4,
      memory_used_bytes: 180_000_000,
      memory_total_bytes: 1_000_000_000,
      disk_used_bytes: 2_000_000_000,
      disk_total_bytes: 8_000_000_000,
      storage: {
        available: true,
        total_bytes: 8_000_000_000,
        used_bytes: 2_000_000_000,
        free_bytes: 6_000_000_000,
        usage_percent: 25,
        level: "normal",
        nonessential_writes_allowed: true,
        durable_writes_allowed: true,
      },
      time_synchronized: true,
      conntrack_count: 120,
      conntrack_max: 131072,
      dhcp_leases: [],
      rx_bytes: 1000,
      tx_bytes: 500,
      ...(overrides.runtime as Record<string, unknown> ?? {}),
    },
    ...overrides,
  };
}

function health(state: string, checks: Array<{ id: string; label: string; state: string; summary: string }>) {
  return { state, headline: `Aggregate state: ${state}`, checks, generated_at: new Date().toISOString() };
}

const HEALTHY_CHECKS = [
  { id: "dns_dhcp", label: "DNS and DHCP", state: "healthy", summary: "dnsmasq is serving" },
  { id: "storage", label: "Storage", state: "healthy", summary: "25% used" },
];

async function stubApi(page: Page, options: {
  system?: Record<string, unknown>;
  healthBody?: unknown;
  healthStatus?: number;
} = {}) {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

  await page.route("**/api/v1/auth/session", (route) => json(route, { authenticated: true, csrf_token: "test" }));
  await page.route("**/api/v1/setup/status", (route) => json(route, { first_run: false }));
  await page.route("**/api/v1/config", (route) => json(route, CONFIG));
  await page.route("**/api/v1/system", (route) => json(route, options.system ?? systemStatus()));
  await page.route("**/api/v1/gateway/summary", (route) =>
    json(route, { available: true, enabled: true, state: "healthy", link: { connected: true, interface: "ppp0" }, packet_loss_percent: 0, pppoe_uptime_seconds: 3600, reconnects_1h: 0, reconnects_24h: 0 }));
  await page.route("**/api/v1/gateway/settings", (route) => json(route, { enabled: true, targets: ["1.1.1.1", "8.8.8.8"], interval_seconds: 30 }));
  await page.route("**/api/v1/gateway/history**", (route) => json(route, { window: "1h", points: [] }));
  await page.route("**/api/v1/snapshots", (route) => json(route, []));
  await page.route("**/api/v1/transactions/pending", (route) => json(route, {}));
  await page.route("**/api/v1/audit/events**", (route) => json(route, { events: [] }));
  await page.route("**/api/v1/accounting**", (route) => json(route, { available: true, enabled: false, months: [], updated_at: new Date().toISOString() }));
  await page.route("**/api/v1/health", (route) => {
    if (options.healthStatus && options.healthStatus >= 400) {
      return route.fulfill({ status: options.healthStatus, contentType: "application/json", body: "{}" });
    }
    return json(route, options.healthBody ?? health("healthy", HEALTHY_CHECKS));
  });
}

test("Overview renders the appliance health banner from /api/v1/health", async ({ page }) => {
  await stubApi(page);
  await page.goto("/");
  const banner = page.getByRole("region", { name: "Appliance health", exact: true });
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("All systems normal");
  await expect(banner).toContainText("2 checks passing");
});

test("a degraded DNS check turns the DNS chip away from green", async ({ page }) => {
  await stubApi(page, {
    healthBody: health("degraded", [
      { id: "dns_dhcp", label: "DNS and DHCP", state: "degraded", summary: "dnsmasq is not responding" },
      { id: "storage", label: "Storage", state: "healthy", summary: "25% used" },
    ]),
  });
  await page.goto("/");
  const chip = page.locator(".overview-service-chip", { hasText: "DNS" }).first();
  await expect(chip).toBeVisible();
  await expect(chip).not.toHaveClass(/is-good/);
  await expect(page.getByRole("region", { name: "Appliance health", exact: true })).toContainText("Degraded");
});

test("an unreachable health endpoint reports unknown rather than healthy", async ({ page }) => {
  await stubApi(page, { healthStatus: 503 });
  await page.goto("/");
  const banner = page.getByRole("region", { name: "Appliance health", exact: true });
  await expect(banner).toContainText("Appliance health unavailable");
  await expect(banner).not.toContainText("All systems normal");
});

// A 200 response is not proof of a readable body. A payload without `checks`
// used to reach the render as health.checks === undefined, throw inside React,
// and unmount the entire dashboard -- the user lost every page, not just the
// health banner, because one endpoint answered oddly.
test("a health payload without checks reads as unavailable and never blanks the dashboard", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await stubApi(page, { healthBody: { state: "healthy", headline: "Aggregate state: healthy" } });
  await page.goto("/");
  const banner = page.getByRole("region", { name: "Appliance health", exact: true });
  await expect(banner).toContainText("Appliance health unavailable");
  await expect(banner).not.toContainText("All systems normal");
  // The rest of the shell must survive: the router is still configurable.
  await expect(page.locator(".dashboard-app")).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test("critical storage pressure is surfaced before saves start failing", async ({ page }) => {
  await stubApi(page, {
    system: systemStatus({
      runtime: {
        disk_used_bytes: 7_600_000_000,
        disk_total_bytes: 8_000_000_000,
        storage: {
          available: true,
          total_bytes: 8_000_000_000,
          used_bytes: 7_600_000_000,
          free_bytes: 400_000_000,
          usage_percent: 95,
          level: "critical",
          nonessential_writes_allowed: false,
          durable_writes_allowed: false,
        },
      },
    }),
  });
  await page.goto("/");
  await expect(page.getByText(/Storage critical/)).toBeVisible();
  await expect(page.locator(".overview-service-chip", { hasText: /Storage 95/ })).toBeVisible();
  // The resource note must not still claim everything is normal.
  await expect(page.locator(".overview-resource-note")).not.toContainText("Within normal operating range");
});

test("recovery state is shown and the notification bell reports real alerts", async ({ page, isMobile }) => {
  await stubApi(page, {
    system: systemStatus({ recovery_required: true, recovery_reason: "boot reconciliation was not verified" }),
    healthBody: health("recovery_required", [
      { id: "transaction", label: "Configuration state", state: "recovery_required", summary: "canonical reconciliation is required" },
      { id: "dns_dhcp", label: "DNS and DHCP", state: "healthy", summary: "dnsmasq is serving" },
    ]),
  });
  await page.goto("/");
  // Assert the banner rather than the first match for the text: the topbar pill
  // also says "Recovery required" but is display:none below 760px, so matching
  // it first made this check depend on the viewport.
  await expect(page.getByRole("region", { name: "Appliance health", exact: true })).toContainText("Recovery required");
  // The topbar bell is hidden below 760px, where the health banner asserted
  // above is the alert surface, so only the desktop layout has a bell to check.
  if (!isMobile) {
    const bell = page.getByRole("button", { name: /Notifications/ });
    await expect(bell).toHaveAttribute("aria-label", /Recovery required/);
    await expect(bell).not.toHaveAttribute("aria-label", /No active appliance alerts/);
  }
});
