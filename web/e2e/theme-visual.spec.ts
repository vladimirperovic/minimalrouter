import { expect, test, type Page, type Route } from "@playwright/test";

/*
 * The visual contract for the dashboard's single Studio look.
 *
 * This exists because the stylesheet consolidation deletes layers that other
 * layers were quietly depending on. Every section is captured here first, so a
 * rule that turns out to have been load-bearing shows up as a pixel diff
 * instead of as something an operator notices months later.
 *
 * Run it with `pnpm --dir web test:visual`, never with playwright directly:
 * the preview server serves dist/, so without a build first this compares the
 * previous build against itself and passes while proving nothing.
 *
 * Baselines are committed. Regenerate deliberately, never to make a red run
 * green:  pnpm --dir web test:visual -- --update-snapshots
 *
 * The whole API is stubbed with documentation-range values (RFC 5737 addresses,
 * RFC 7042 MACs, RFC 2606 names), so a capture never depends on an appliance or
 * on the time of day.
 */

// An absolute budget, not a ratio. A ratio of 0.002 sounds strict but allows
// ~5,000 pixels on a 1440x1800 capture - enough to hide an entire toolbar
// button, which is exactly what it did the first time this ran. This tolerates
// font antialiasing and nothing structural.
const MAX_DIFF_PIXELS = 200;

const NOW = new Date("2026-03-14T09:41:00Z");

const CONFIG = {
  revision: 42,
  updated_at: NOW.toISOString(),
  system: { hostname: "minimalrouter", domain: "lan", timezone: "UTC", https_port: 8443, management_access: "lan_only" },
  wan: { enabled: true, mode: "pppoe", interface: "eth0", username: "subscriber@example.net", password: "[REDACTED]", mtu: 1492 },
  lan: { interface: "eth1", ip_address: "192.168.1.1", cidr: "192.168.1.1/24" },
  dhcp: {
    enabled: true, start: "192.168.1.100", end: "192.168.1.200", lease_time: "12h",
    dns_servers: ["1.1.1.1", "9.9.9.9"],
    static_leases: [
      { id: "l1", hostname: "workstation", mac: "00:00:5e:00:53:10", ip_address: "192.168.1.40" },
      { id: "l2", hostname: "printer", mac: "00:00:5e:00:53:11", ip_address: "192.168.1.41" },
    ],
  },
  firewall: {
    default_policy: "drop", allow_ping: true,
    port_forwards: [
      { id: "pf1", name: "web", protocol: "tcp", external_port: 443, internal_ip: "192.168.1.40", internal_port: 8443, enabled: true },
    ],
    rules: [],
  },
  wireguard: { enabled: true, interface: "wg0", address: "10.7.0.1/24", listen_port: 51820, private_key: "[REDACTED]", peers: [] },
  wg_client: { enabled: false, interface: "wg1", private_key: "[REDACTED]", preshared_key: "[REDACTED]" },
  qos: { enabled: true, download_mbps: 540, upload_mbps: 360 },
  adguard: { enabled: true, filter_devices: [] },
  cloudflare: { enabled: false, api_token: "[REDACTED]", tunnel_token: "[REDACTED]" },
  squid_proxy: { enabled: false, password: "[REDACTED]" },
  wifi: { enabled: false, passphrase: "[REDACTED]" },
  accounting: { enabled: true, retention_months: 13 },
  trusted_networks: ["192.168.1.0/24"],
};

const SYSTEM = {
  version: "0.1.7",
  runtime: {
    uptime_seconds: 691_200, memory_total_bytes: 1_073_741_824, memory_used_bytes: 186_646_528,
    disk_total_bytes: 8_589_934_592, disk_used_bytes: 2_576_980_377,
    rx_bytes: 91_000_000_000, tx_bytes: 12_000_000_000,
    public_ip: "203.0.113.9", wan_connected: true, dhcp_leases: [],
    load_average: [0.14, 0.11, 0.09], cpu_idle_percent: 93,
  },
};

const HEALTH = { checks: [], overall: "healthy" };
const GATEWAY = {
  available: true, enabled: true, state: "healthy", timestamp: NOW.toISOString(),
  link: { connected: true, local_ip: "203.0.113.9", peer_ip: "203.0.113.1" },
  latency_ms: 18, jitter_ms: 2, packet_loss_percent: 0, pppoe_uptime_seconds: 691_200,
  reconnects_1h: 0, reconnects_24h: 0, targets: [],
};

async function stub(page: Page) {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

  // Playwright matches the most recently registered handler first, so the
  // catch-all goes first and every specific stub below overrides it. Reversed,
  // it would swallow /api/v1/config and the dashboard would never render.
  await page.route("**/api/v1/**", (r) => json(r, {}));
  await page.route("**/api/v1/auth/session", (r) => json(r, { authenticated: true, csrf_token: "visual", read_only: false }));
  await page.route("**/api/v1/setup/status", (r) => json(r, { is_configured: true }));
  await page.route("**/api/v1/config", (r) => json(r, CONFIG));
  await page.route("**/api/v1/system", (r) => json(r, SYSTEM));
  await page.route("**/api/v1/health", (r) => json(r, HEALTH));
  await page.route("**/api/v1/gateway/summary", (r) => json(r, GATEWAY));
  await page.route("**/api/v1/gateway/history", (r) => json(r, { available: true, points: [] }));
  await page.route("**/api/v1/gateway/settings", (r) => json(r, { enabled: true, interval_seconds: 30, targets: ["1.1.1.1"] }));
  // The full shape matters: the panel reads uptime_percent unconditionally once
  // available is true, and a partial stub takes the whole dashboard down.
  await page.route("**/api/v1/gateway/insights", (r) => json(r, {
    available: true, sampled_hours: 0, uptime_percent: 100, outages: 0,
    longest_outage_minutes: 0, public_ips: [],
  }));
  await page.route("**/api/v1/snapshots", (r) => json(r, { snapshots: [] }));
  await page.route("**/api/v1/audit/events", (r) => json(r, { events: [] }));
  await page.route("**/api/v1/transactions/pending", (r) => json(r, { pending: false }));
  await page.route("**/api/v1/accounting**", (r) => json(r, { available: true, enabled: true, months: [] }));
  await page.route("**/api/v1/firmware/**", (r) => json(r, {
    enabled: true, current_version: "0.1.7", running_version: "0.1.7", channel: "beta",
    update_available: false, can_install: false, blocked_reason: "already_current",
    last_successful_check_at: NOW.toISOString(), stale: false,
  }));
}

const SECTIONS = [
  "overview", "gateway", "network", "firewall", "security", "dns-filter",
  "qos", "wireguard", "cloudflare", "wifi", "traffic", "squid", "recovery", "logs",
] as const;

// A tall viewport with fullPage disabled: the sidebar and top bar are fixed, and
// a fullPage capture re-renders them at their scroll offset.
test.use({ viewport: { width: 1440, height: 1800 } });

test.skip(
  ({ browserName, isMobile }) => browserName !== "chromium" || isMobile,
  "the visual contract is captured on desktop chromium only",
);

async function openDashboard(page: Page) {
  // Until Studio becomes the only look, the baseline has to ask for it
  // explicitly. Afterwards this is a harmless no-op, which is what makes the
  // before/after images comparable.
  await page.addInitScript(() => {
    try {
      window.localStorage.setItem("minimalrouter:skin", "studio");
    } catch {
      /* private-mode browsers still render the default look */
    }
  });
  await stub(page);
  await page.goto("/");
  // The sidebar is off-canvas on phones, so wait for the shell itself.
  await page.waitForSelector(".dashboard-main", { timeout: 15_000 });
  // The live bandwidth chart animates for a moment after the first samples.
  await page.waitForTimeout(1_500);
}

test.describe("Studio look", () => {
  for (const section of SECTIONS) {
    test(`section ${section}`, async ({ page }) => {
      const crashes: string[] = [];
      page.on("pageerror", (event) => crashes.push(event.message));
      await openDashboard(page);

      const link = page.locator(`a[href="#${section}"]`).first();
      await link.waitFor({ state: "visible" });
      await link.click();
      // Assert on the element that was clicked. A document-wide query can find
      // a different link to the same section and wait forever on it.
      await expect(link).toHaveClass(/is-active/);
      await page.waitForTimeout(500);
      await page.evaluate(() => window.scrollTo(0, 0));

      await expect(page).toHaveScreenshot(`${section}.png`, {
        // The bandwidth sparkline and the clock in the top bar move on their
        // own; masking them keeps the contract about layout and colour.
        mask: [page.locator(".classic-live-sync"), page.locator("canvas")],
        maxDiffPixels: MAX_DIFF_PIXELS,
        animations: "disabled",
      });
      expect(crashes, "the dashboard threw while capturing").toEqual([]);
    });
  }

  // Every section at phone width too. The mobile stylesheets are folded into
  // component media queries next, and two captures could not tell whether a
  // page survived it.
  for (const section of SECTIONS) {
    test(`phone ${section}`, async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 844 });
      await openDashboard(page);
      const menu = page.locator(".dashboard-menu:visible").first();
      await menu.click();
      const link = page.locator(`a[href="#${section}"]`).first();
      await link.waitFor({ state: "visible" });
      await link.click();
      await expect(link).toHaveClass(/is-active/);
      await page.waitForTimeout(500);
      await page.evaluate(() => window.scrollTo(0, 0));

      // A phone capture is full-page: what matters is whether the whole
      // section fits the width and stacks, not just its first screen.
      await expect(page).toHaveScreenshot(`phone-${section}.png`, {
        mask: [page.locator(".classic-live-sync"), page.locator("canvas")],
        maxDiffPixels: MAX_DIFF_PIXELS,
        animations: "disabled",
        fullPage: true,
      });

      // Nothing may push the page sideways at 390px.
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(overflow, "the page scrolls horizontally at phone width").toBeLessThanOrEqual(0);
    });
  }

  test("dark form", async ({ page }) => {
    await openDashboard(page);
    await page.getByRole("button", { name: /toggle appearance/i }).click();
    await page.waitForTimeout(400);
    await expect(page).toHaveScreenshot("overview-dark.png", {
      mask: [page.locator(".classic-live-sync"), page.locator("canvas")],
      maxDiffPixels: MAX_DIFF_PIXELS,
      animations: "disabled",
    });
  });

  test("mobile overview", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openDashboard(page);
    await expect(page).toHaveScreenshot("overview-mobile.png", {
      mask: [page.locator(".classic-live-sync"), page.locator("canvas")],
      maxDiffPixels: MAX_DIFF_PIXELS,
      animations: "disabled",
      fullPage: true,
    });
  });

  // The sign-in screen is its own surface and must carry the same look.
  test("sign in", async ({ page }) => {
    const json = (route: Route, body: unknown, status = 200) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    await page.route("**/api/v1/**", (r) => json(r, {}));
    await page.route("**/api/v1/auth/session", (r) => json(r, { error: "unauthorized" }, 401));
    await page.route("**/api/v1/setup/status", (r) => json(r, { is_configured: true }));
    await page.goto("/");
    await page.waitForSelector("input[type=password]", { timeout: 15_000 });
    await page.waitForTimeout(400);
    await expect(page).toHaveScreenshot("sign-in.png", {
      maxDiffPixels: MAX_DIFF_PIXELS,
      animations: "disabled",
    });
  });

  test("sign in on mobile", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const json = (route: Route, body: unknown, status = 200) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    await page.route("**/api/v1/**", (r) => json(r, {}));
    await page.route("**/api/v1/auth/session", (r) => json(r, { error: "unauthorized" }, 401));
    await page.route("**/api/v1/setup/status", (r) => json(r, { is_configured: true }));
    await page.goto("/");
    await page.waitForSelector("input[type=password]", { timeout: 15_000 });
    await page.waitForTimeout(400);
    await expect(page).toHaveScreenshot("sign-in-mobile.png", {
      maxDiffPixels: MAX_DIFF_PIXELS,
      animations: "disabled",
    });
  });

  test("mobile navigation drawer", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openDashboard(page);
    // Two navigation toggles exist: the one DashboardApp renders and the one
    // MobileNavigationBehavior mounts on top of it. Drive the visible one.
    const menu = page.locator(".dashboard-menu:visible").first();
    await menu.waitFor({ state: "visible" });
    await menu.click();
    await page.waitForTimeout(400);
    await expect(page).toHaveScreenshot("mobile-drawer.png", {
      mask: [page.locator(".classic-live-sync"), page.locator("canvas")],
      maxDiffPixels: MAX_DIFF_PIXELS,
      animations: "disabled",
    });
  });
});
