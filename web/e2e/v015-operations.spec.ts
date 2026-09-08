import { expect, test, type Page, type Route } from "@playwright/test";
import type { RouterConfig, StaticLease } from "../src/api-types";

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
  await page.route("**/api/v1/**", (route) => json(route, {}));
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
  await page.route("**/api/v1/startup/boots", (route) => json(route, { boots: [{ id: "test-boot", started_at: NOW.toISOString(), completed: true, readiness: { management_seconds: 2, pppoe_seconds: 4, dns_seconds: 3, internet_seconds: 5, wireguard_seconds: 6 } }] }));
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

test("Known devices shows activity and sends a timed Internet pause", async ({ page, isMobile }) => {
  await stubDashboard(page);
  let pauseRequest: { ip?: string; seconds?: number } = {};
  await page.route("**/api/v1/devices/pause", async (route) => {
    pauseRequest = route.request().postDataJSON() as { ip?: string; seconds?: number };
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ success: true, pauses: [{ ip: "192.168.1.60", until_unix: Math.floor(Date.now() / 1000) + 900 }] }) });
  });

  await page.goto("/");
  await openSection(page, isMobile, "LAN & DHCP");
  const row = page.getByRole("row", { name: /Kids iPad/ });
  await expect(row).toContainText("DHCP lease");
  await expect(row).not.toContainText("Online");
  await expect(row).toContainText("New");
  await row.getByRole("button", { name: "Pause Internet" }).click();
  await page.getByRole("button", { name: "15 min" }).click();
  await expect.poll(() => pauseRequest).toEqual({ ip: "192.168.1.60", seconds: 900 });
  await expect(row).toContainText("Paused");
  await expect(row.getByRole("button", { name: "Resume" })).toBeVisible();
});

for (const accountingEnabled of [false, true]) {
  test(`DHCP leases do not imply presence after a MAC change (accounting ${accountingEnabled})`, async ({ page, isMobile }, testInfo) => {
    await stubDashboard(page);
    const oldLease = { hostname: "test-laptop", mac: "02:00:00:00:00:14", ip_address: "192.168.1.14", expires_at: CURRENT_EPOCH + 43_200 };
    const newLease = { hostname: "", mac: "02:00:00:00:00:36", ip_address: "192.168.1.236", expires_at: 0 };
    const config = structuredClone(CONFIG) as RouterConfig;
    config.accounting = { ...CONFIG.accounting, enabled: accountingEnabled };
    config.dhcp.static_leases = [{ id: "test-reservation", hostname: oldLease.hostname, mac: oldLease.mac, ip_address: oldLease.ip_address }];
    await page.route("**/api/v1/config", (route) => route.fulfill({ json: config }));
    await page.route("**/api/v1/system", (route) => route.fulfill({ json: { ...SYSTEM, runtime: { ...SYSTEM.runtime, dhcp_leases: [oldLease, newLease] } } }));
    await page.goto("/");
    await openSection(page, isMobile, "LAN & DHCP");
    const table = page.locator(".modern-device-section");
    await expect(table.getByRole("heading", { name: "Known devices" })).toBeVisible();
    await expect(table).toContainText("2 DHCP leases · 1 static reservation");
    await expect(table).toContainText("It does not confirm that the device is online.");
    const oldRow = table.getByRole("row", { name: /test-laptop/ });
    await expect(oldRow).toContainText("192.168.1.14");
    await expect(oldRow).toContainText("DHCP lease · expires in");
    await expect(oldRow).not.toContainText(/Online|Last seen|just now/);
    await expect(oldRow.getByRole("button", { name: "Wake test-laptop" })).toBeVisible();
    const newRow = table.getByRole("row", { name: /192\.168\.1\.236/ });
    await expect(newRow).toContainText("DHCP lease · no expiry");
    await expect(newRow).not.toContainText("Static");
    for (const row of [oldRow, newRow]) {
      const status = await row.locator(".device-activity-state").boundingBox();
      const action = await row.locator(".device-row-actions button").first().boundingBox();
      expect(status).not.toBeNull();
      expect(action).not.toBeNull();
      expect(status!.x + status!.width <= action!.x || status!.y + status!.height <= action!.y).toBe(true);
    }
    await expect(table.locator(".is-online, .is-offline")).toHaveCount(0);
    if (accountingEnabled) {
      const history = table.getByRole("row", { name: /Kids iPad/ });
      await expect(history).toContainText("Last seen");
      await expect(history).not.toContainText(/Online|Offline|DHCP lease/);
    }
    if (!accountingEnabled) await table.screenshot({ path: testInfo.outputPath("dhcp-presence.png") });
  });
}

test("LAN tables fit laptop and phone widths without horizontal scroll or clipped actions", async ({ page, isMobile }, testInfo) => {
  await stubDashboard(page);
  if (!isMobile) await page.setViewportSize({ width: 1280, height: 900 });
  const config = structuredClone(CONFIG) as RouterConfig;
  config.dhcp.static_leases = Array.from({ length: 12 }, (_, i) => ({ id: `test-${i}`, hostname: `Office computer with a long name ${i}`, mac: `02:00:00:00:00:${(i + 20).toString(16)}`, ip_address: `192.168.1.${i + 20}` }));
  await page.route("**/api/v1/config", (route) => route.fulfill({ json: config }));
  await page.goto("/");
  await openSection(page, isMobile, "LAN & DHCP");
  await expect(page.locator(".static-leases tbody tr")).toHaveCount(12);
  for (const selector of [".modern-device-section", ".static-leases"]) {
    const card = page.locator(selector);
    const overflow = await card.evaluate((element) => {
      const container = element.querySelector(".elegant-table-container")!;
      const boundary = container.getBoundingClientRect();
      const actions = [...container.querySelectorAll("button")].map((button) => button.getBoundingClientRect());
      return {
        scroll: container.scrollWidth - container.clientWidth,
        clippedActions: actions.some((rect) => rect.left < boundary.left || rect.right > boundary.right + 1),
        clippedLastRow: container.querySelector("tbody tr:last-child")!.getBoundingClientRect().bottom > boundary.bottom + 1,
      };
    });
    expect(overflow).toEqual({ scroll: 0, clippedActions: false, clippedLastRow: false });
  }
  await page.locator(".static-leases").screenshot({ path: testInfo.outputPath("reservations-fit.png") });
  await page.locator(".modern-device-section").screenshot({ path: testInfo.outputPath("devices-fit.png") });
});

for (const name of [" office-tablet ", ""]) {
  test(`Reserve IP saves ${name ? "an edited" : "an optional empty"} device name and keeps concurrent reservations`, async ({ page, isMobile }, testInfo) => {
    await stubDashboard(page);
    let config = { ...structuredClone(CONFIG), dhcp: { ...CONFIG.dhcp, static_leases: [] as StaticLease[] } };
    let submitted: RouterConfig | undefined;
    await page.route("**/api/v1/config", async (route) => {
      if (route.request().method() === "PUT") {
        submitted = route.request().postDataJSON() as RouterConfig;
        config = { ...config, dhcp: submitted.dhcp };
      }
      await route.fulfill({ json: config });
    });
    await page.route("**/api/v1/config/preview", (route) => route.fulfill({ json: { changes: ["Reserve device address"], risk: "low", requires_confirmation: false } }));
    page.on("dialog", (dialog) => dialog.accept());

    await page.goto("/");
    await openSection(page, isMobile, "LAN & DHCP");
    await page.getByRole("button", { name: "Reserve an IP address for Kids iPad" }).click();
    const dialog = page.getByRole("dialog", { name: "Reserve an IP for Kids iPad" });
    const deviceName = dialog.getByLabel("Device name");
    await expect(deviceName).toHaveValue("Kids iPad");
    await expect(dialog.getByRole("button", { name: "Reserve IP", exact: true })).toBeDisabled();
    await deviceName.fill("temporary-name");
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await page.getByRole("button", { name: "Reserve an IP address for Kids iPad" }).click();
    await expect(deviceName).toHaveValue("Kids iPad");
    await deviceName.fill(name);
    await dialog.getByLabel("Reserved IPv4 address").fill("192.168.1.14");
    await expect(dialog.getByLabel("MAC address", { exact: true })).toHaveAttribute("readonly", "");
    await dialog.screenshot({ path: testInfo.outputPath("reservation.png") });

    // Another session saved a reservation after the dashboard's cached GET.
    const concurrent = { id: "printer", hostname: "printer", mac: "00:00:5e:00:53:14", ip_address: "192.168.1.15" };
    config.dhcp.static_leases.push(concurrent);
    await dialog.getByRole("button", { name: "Reserve IP", exact: true }).click();
    await expect.poll(() => submitted?.dhcp.static_leases).toEqual([
      concurrent,
      { id: expect.any(String), hostname: name.trim(), mac: "00:00:5e:00:53:13", ip_address: "192.168.1.14" },
    ]);
    await expect(dialog).not.toBeVisible();
    const connected = page.locator(".modern-device-section");
    await expect(connected.getByRole("row", { name: name ? /office-tablet/ : /Kids iPad/ })).toContainText("Static");
    await expect(page.locator(".static-leases").getByRole("row", { name: name ? /office-tablet/ : /Unnamed device/ })).toContainText("192.168.1.14");
  });
}

test("Every device search keeps the icon clear of placeholder and entered text", async ({ page, isMobile }) => {
  await stubDashboard(page);
  await page.goto("/");
  for (const section of ["Overview", "LAN & DHCP"]) {
    if (section !== "Overview") await openSection(page, isMobile, section);
    const searches = page.locator(".modern-search-wrapper");
    await expect(searches).toHaveCount(section === "Overview" ? 1 : 2);
    for (const search of await searches.all()) {
      const input = search.locator("input");
      for (const text of ["", "tablet"]) {
        await input.fill(text);
        const gap = await search.evaluate((element) => {
          const input = element.querySelector("input")!;
          const icon = element.querySelector("svg")!;
          const style = getComputedStyle(input);
          return input.getBoundingClientRect().left + parseFloat(style.borderLeftWidth) + parseFloat(style.paddingLeft) - icon.getBoundingClientRect().right;
        });
        expect(gap).toBeGreaterThanOrEqual(8);
      }
    }
  }
});

for (const theme of ["light", "dark"]) {
  test(`Overview arranges four cards in two rows and keeps boot text readable in ${theme} mode`, async ({ page, isMobile }, testInfo) => {
    await stubDashboard(page);
    await page.addInitScript((theme) => localStorage.setItem("minimalrouter:theme", theme), theme);
    if (!isMobile) await page.setViewportSize({ width: 1600, height: 1100 });
    await page.goto("/");
    const grid = page.locator(".overview-content-grid");
    await expect(grid.locator(":scope > section > header h2")).toHaveText(["Live bandwidth", "Appliance resources", "Gateway quality", "Boot activity"]);
    const cards = grid.locator(":scope > section");
    const boxes = await Promise.all([0, 1, 2, 3].map((index) => cards.nth(index).boundingBox()));
    expect(boxes.every(Boolean)).toBe(true);
    if (isMobile) {
      expect(boxes[0]!.y + boxes[0]!.height).toBeLessThanOrEqual(boxes[1]!.y);
      expect(boxes[1]!.y + boxes[1]!.height).toBeLessThanOrEqual(boxes[2]!.y);
      expect(boxes[2]!.y + boxes[2]!.height).toBeLessThanOrEqual(boxes[3]!.y);
    } else {
      expect(Math.abs(boxes[0]!.y - boxes[1]!.y)).toBeLessThan(1);
      expect(Math.abs(boxes[2]!.y - boxes[3]!.y)).toBeLessThan(1);
      expect(Math.abs(boxes[0]!.x - boxes[2]!.x)).toBeLessThan(1);
      expect(Math.abs(boxes[1]!.x - boxes[3]!.x)).toBeLessThan(1);
      expect(boxes[0]!.y + boxes[0]!.height).toBeLessThanOrEqual(boxes[2]!.y);
      expect(boxes[1]!.y + boxes[1]!.height).toBeLessThanOrEqual(boxes[3]!.y);
      expect(boxes[0]!.x + boxes[0]!.width).toBeLessThanOrEqual(boxes[1]!.x);
      expect(boxes[2]!.x + boxes[2]!.width).toBeLessThanOrEqual(boxes[3]!.x);
    }
    const terminal = page.locator(".boot-terminal");
    await expect(terminal.getByText("System", { exact: true })).toBeVisible();
    const ratios = await terminal.evaluate((element) => {
      const luminance = (color: string) => {
        const channels = color.match(/[\d.]+/g)!.slice(0, 3).map(Number).map((v) => v / 255).map((v) => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
        return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
      };
      const background = luminance(getComputedStyle(element).backgroundColor);
      return [...element.querySelectorAll("code, code b, .boot-terminal-time")].map((text) => {
        const foreground = luminance(getComputedStyle(text).color);
        return (Math.max(background, foreground) + 0.05) / (Math.min(background, foreground) + 0.05);
      });
    });
    expect(Math.min(...ratios)).toBeGreaterThanOrEqual(4.5);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
    await grid.screenshot({ path: testInfo.outputPath("overview-cards.png") });
  });
}
