import { expect, test, type Page, type Route } from "@playwright/test";

const CONFIG = {
  revision: 42,
  updated_at: new Date().toISOString(),
  system: { hostname: "minimalrouter", domain: "lan", https_enabled: true, https_port: 8443, management_access: "lan_and_wireguard" },
  wan: { interface: "eth0", enabled: true, username: "isp", mtu: 1492 },
  lan: { interface: "eth1", ip_address: "192.168.1.1", netmask: "255.255.255.0", cidr: "192.168.1.1/24" },
  dhcp: { enabled: true, dns_enabled: false, range_start: "192.168.1.100", range_end: "192.168.1.200", lease_time: "12h", dns_servers: ["1.1.1.1"], static_leases: [] },
  dns: { records: [] },
  firewall: { default_wan_input_policy: "deny", wan_ingress_mode: "wireguard_only", stateful_firewall: true, port_forwards: [], custom_rules: [], extra_lans: [] },
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

async function stubDashboard(page: Page) {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

  await page.route("**/api/v1/auth/session", (route) => json(route, { authenticated: true, csrf_token: "test" }));
  await page.route("**/api/v1/config", (route) => json(route, CONFIG));
  await page.route("**/api/v1/system", (route) => json(route, {
    status: "Connected",
    revision: 42,
    runtime: {
      available: true,
      wan_connected: true,
      memory_used_bytes: 100,
      memory_total_bytes: 1000,
      disk_used_bytes: 100,
      disk_total_bytes: 1000,
      dhcp_leases: [],
    },
  }));
  await page.route("**/api/v1/gateway/summary", (route) => json(route, {
    available: true,
    enabled: true,
    state: "healthy",
    link: { connected: true, interface: "ppp0" },
  }));
  await page.route("**/api/v1/gateway/settings", (route) => json(route, { enabled: true, targets: ["1.1.1.1"], interval_seconds: 30 }));
  await page.route("**/api/v1/snapshots", (route) => json(route, []));
  await page.route("**/api/v1/transactions/pending", (route) => json(route, {}));
  await page.route("**/api/v1/health", (route) => json(route, { state: "healthy", headline: "healthy", checks: [], generated_at: new Date().toISOString() }));
  await page.route("**/api/v1/audit/events**", (route) => json(route, { events: [] }));
}

async function openSection(page: Page, isMobile: boolean | undefined, name: string) {
  if (isMobile) {
    await page.getByRole("button", { name: "Open navigation" }).click();
  }
  await page.getByRole("link", { name }).click();
}

test("Security owns TOTP while Recovery owns backup, migration and diagnostics", async ({ page, isMobile }) => {
  await stubDashboard(page);
  await page.goto("/");
  await openSection(page, isMobile, "Security");
  await expect(page.getByRole("heading", { name: "Two-factor authentication" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Recovery tools" })).toBeHidden();

  await openSection(page, isMobile, "Recovery");
  await expect(page.getByRole("heading", { name: "Recovery tools" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Download diagnostics" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Export encrypted backup" })).toBeVisible();
  await page.getByText("Migrate from pfSense config.xml").click();
  await expect(page.getByRole("button", { name: "Preview pfSense migration" })).toBeVisible();
});

test("TOTP enrollment uses password-gated preview before enable", async ({ page, isMobile }) => {
  await stubDashboard(page);
  await page.route("**/api/v1/auth/totp/enroll", (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      enabled: false,
      secret: "JBSWY3DPEHPK3PXP",
      qr_uri: "otpauth://totp/MinimalRouter:admin?secret=JBSWY3DPEHPK3PXP",
    }),
  }));

  await page.goto("/");
  await openSection(page, isMobile, "Security");
  const panel = page.locator('[aria-labelledby="totp-settings-title"]');
  await panel.locator('input[name="current_password"]').fill("test-password");
  await panel.getByRole("button", { name: "Start 2FA enrollment" }).click();
  await expect(panel.locator('input[readonly]').first()).toHaveValue("JBSWY3DPEHPK3PXP");
  await expect(panel.getByRole("button", { name: "Verify and enable 2FA" })).toBeVisible();
});

test("pfSense migration is previewed with warnings before apply", async ({ page, isMobile }) => {
  await stubDashboard(page);
  await page.route("**/api/v1/import/pfsense/preview**", (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({
      import_id: "preview-1",
      expires_in_seconds: 600,
      report: {
        source_version: "23.09",
        imported: { pppoe_accounts: 1, port_forwards: 1 },
        warnings: ["Imported NAT rule as disabled because WireGuard is the only permitted WAN entry point."],
        unsupported_sections: ["VLANs"],
        config: CONFIG,
      },
    }),
  }));

  await page.goto("/");
  await openSection(page, isMobile, "Recovery");
  const panel = page.locator('[aria-labelledby="recovery-tools-title"]');
  await panel.getByText("Migrate from pfSense config.xml").click();
  await panel.locator('input[name="pfsense_xml"]').setInputFiles({
    name: "config.xml",
    mimeType: "application/xml",
    buffer: Buffer.from("<pfsense><version>23.09</version></pfsense>"),
  });
  await panel.getByRole("button", { name: "Preview pfSense migration" }).click();
  await expect(panel.getByText(/Imported NAT rule as disabled/)).toBeVisible();
  await expect(panel.getByText("VLANs")).toBeVisible();
  await expect(panel.getByRole("button", { name: "Apply pfSense migration" })).toBeVisible();
});
