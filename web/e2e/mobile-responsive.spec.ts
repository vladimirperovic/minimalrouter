import { expect, test, type Page, type Route } from "@playwright/test";

const CONFIG = {
  revision: 42,
  updated_at: new Date().toISOString(),
  system: { hostname: "minimalrouter", domain: "lan", https_enabled: true, https_port: 8443, management_access: "lan_and_wireguard" },
  wan: { interface: "eth0", enabled: true, username: "isp", mtu: 1492 },
  lan: { interface: "eth1", ip_address: "192.168.1.1", netmask: "255.255.255.0", cidr: "192.168.1.1/24" },
  dhcp: { enabled: true, dns_enabled: true, range_start: "192.168.1.100", range_end: "192.168.1.200", lease_time: "12h", dns_servers: ["1.1.1.1"], static_leases: [] },
  dns: { records: [] },
  firewall: { default_wan_input_policy: "deny", wan_ingress_mode: "wireguard_only", stateful_firewall: true, port_forwards: [], custom_rules: [] },
  wireguard: { enabled: true, interface: "wg0", listen_port: 51820, address: "10.8.0.1/24", peers: [] },
  wg_client: { enabled: false, interface: "wg1", address: "", public_key: "", endpoint: "", allowed_ips: [], persistent_keepalive: 25 },
  cloudflare: { ddns_enabled: true, ddns_provider: "noip", domain: "router.example.net", tunnel_enabled: false },
  squid_proxy: { enabled: true, port: 3128, username: "proxyadmin", restricted_ips: [] },
  adguard: { enabled: true, blocklist_url: "", last_updated: "Never", device_profiles: [] },
  qos: { enabled: true, algorithm: "cake", download_limit_mbps: 600, upload_limit_mbps: 400 },
  accounting: { enabled: false, retention_months: 13 },
  wifi: { enabled: false, interface: "wlan0", ssid: "MinimalRouter-Home", band: "5ghz", channel: 36, hide_ssid: false },
  trusted_networks: ["192.168.1.0/24"],
};

const SYSTEM = {
  status: "Connected",
  version: "v0.1.5-beta",
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
  },
};

const SECTIONS = [
  ["overview", "Overview"],
  ["gateway", "Gateway Quality"],
  ["network", "LAN & DHCP"],
  ["firewall", "Firewall"],
  ["security", "Security"],
  ["dns-filter", "DNS Filter"],
  ["qos", "QoS / SQM"],
  ["wireguard", "WireGuard"],
  ["cloudflare", "DynDNS"],
  ["wifi", "Wi-Fi AP"],
  ["traffic", "Traffic"],
  ["squid", "Squid Proxy"],
  ["recovery", "Recovery"],
  ["logs", "Logs"],
] as const;

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

async function stubApi(page: Page) {
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/auth/session") return json(route, { authenticated: true, csrf_token: "test" });
    if (path === "/api/v1/setup/status") return json(route, { first_run: false });
    if (path === "/api/v1/config") return json(route, CONFIG);
    if (path === "/api/v1/system") return json(route, SYSTEM);
    if (path === "/api/v1/health") return json(route, {
      state: "healthy",
      headline: "All systems normal",
      checks: [
        { id: "dns_dhcp", label: "DNS and DHCP", state: "healthy", summary: "dnsmasq is serving" },
        { id: "storage", label: "Storage", state: "healthy", summary: "25% used" },
      ],
      generated_at: new Date().toISOString(),
    });
    if (path === "/api/v1/gateway/summary") return json(route, {
      available: true,
      enabled: true,
      state: "healthy",
      link: { connected: true, interface: "ppp0" },
      packet_loss_percent: 0,
      pppoe_uptime_seconds: 3600,
      reconnects_1h: 0,
      reconnects_24h: 0,
    });
    if (path === "/api/v1/gateway/settings") return json(route, { enabled: true, targets: ["1.1.1.1", "8.8.8.8"], interval_seconds: 30 });
    if (path === "/api/v1/gateway/history") return json(route, { window: "1h", points: [] });
    if (path === "/api/v1/snapshots") return json(route, []);
    if (path === "/api/v1/transactions/pending") return json(route, {});
    if (path === "/api/v1/audit/events") return json(route, { events: [] });
    if (path === "/api/v1/accounting") return json(route, { available: true, enabled: false, months: [], updated_at: new Date().toISOString() });
    if (path === "/api/v1/wireguard/provisioning-preview") return json(route, { client_ip: "10.8.0.2/32", server_endpoint: "router.example.net:51820" });
    return json(route, {});
  });
}

async function expectNoPageOverflow(page: Page) {
  const geometry = await page.evaluate(() => {
    const main = document.querySelector<HTMLElement>(".dashboard-main")!.getBoundingClientRect();
    return {
      viewport: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      bodyWidth: document.body.scrollWidth,
      mainLeft: main.left,
      mainRight: main.right,
    };
  });
  expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewport + 1);
  expect(geometry.bodyWidth).toBeLessThanOrEqual(geometry.viewport + 1);
  expect(geometry.mainLeft).toBeGreaterThanOrEqual(-1);
  expect(geometry.mainRight).toBeLessThanOrEqual(geometry.viewport + 1);
}

test.use({ viewport: { width: 390, height: 844 } });

test("mobile navigation fills the viewport and the menu button closes it", async ({ page, isMobile }) => {
  test.skip(!isMobile, "mobile-only responsive regression");
  await stubApi(page);
  await page.goto("/");
  await expect(page.locator(".dashboard-app")).toBeVisible();

  const menu = page.getByRole("button", { name: "Open navigation" });
  const sidebar = page.locator(".dashboard-sidebar");
  await menu.click();
  await page.waitForTimeout(280);

  const box = await sidebar.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  });
  expect(Math.abs(box.x)).toBeLessThanOrEqual(1);
  expect(Math.abs(box.y)).toBeLessThanOrEqual(1);
  expect(box.width).toBeGreaterThanOrEqual(389);
  expect(box.height).toBeGreaterThanOrEqual(843);
  await expect(sidebar).toHaveClass(/is-open/);
  expect(await page.evaluate(() => getComputedStyle(document.body).overflowY)).toBe("hidden");
  expect(await menu.evaluate((element) => getComputedStyle(element, "::before").content)).toContain("×");

  await menu.click();
  await expect(sidebar).not.toHaveClass(/is-open/);
});

test("every dashboard section stays inside the mobile viewport", async ({ page, isMobile }) => {
  test.skip(!isMobile, "mobile-only responsive regression");
  await stubApi(page);
  await page.goto("/");
  await expect(page.locator(".dashboard-app")).toBeVisible();

  for (const [id, label] of SECTIONS) {
    if (id !== "overview") {
      await page.getByRole("button", { name: "Open navigation" }).click();
      await expect(page.locator(".dashboard-sidebar")).toHaveClass(/is-open/);
      await page.locator(`.dashboard-navigation a[href="#${id}"]`).click();
      await expect(page.locator(".dashboard-sidebar")).not.toHaveClass(/is-open/);
    }
    await expect(page.locator(".classic-page-heading h1")).toHaveText(label);
    await expectNoPageOverflow(page);
  }
});
