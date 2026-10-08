import { test, expect, type Page } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";
import type { DNSRiskAlert, DNSRiskException } from "../src/api-types";

async function fixture(page: Page) {
  let enabled = true, unavailable = false, stale = false;
  let exceptions: DNSRiskException[] = [];
  let alerts: DNSRiskAlert[] = Array.from({ length: 27 }, (_, i) => ({ id: i + 1, domain: `risk-${i + 1}.example`, category: i === 0 ? "adult" : "phishing", severity: i === 0 ? "warning" : "high", first_seen: 1791417600, last_seen: 1791417660, lookups: 8, last_address: "2001:db8::5", acknowledged_at: 0, ignored: false }));
  const mutations: string[] = [];
  await page.addInitScript(() => localStorage.setItem("minimalrouter:wan-speed-estimate-attempt", String(Date.now())));
  await page.route("**/api/v1/**", route => {
    const req = route.request(), url = new URL(req.url()), path = url.pathname, method = req.method();
    const json = (value: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) });
    if (method !== "GET") mutations.push(`${method} ${path}`);
    const responses: Record<string, unknown> = {
      "/api/v1/auth/session": { authenticated: true, csrf_token: "test" }, "/api/v1/config": CONFIG, "/api/v1/system": SYSTEM, "/api/v1/health": HEALTH,
      "/api/v1/gateway/summary": GW_SUMMARY, "/api/v1/gateway/settings": GW_SETTINGS, "/api/v1/snapshots": [], "/api/v1/devices/pauses": { pauses: [] },
    };
    if (path === "/api/v1/dns-activity/settings") {
      if (method === "PUT") { enabled = req.postDataJSON().enabled; if (!enabled) alerts = []; }
      return json({ available: true, enabled, retention_days: 30 });
    }
    if (path === "/api/v1/dns-activity/alerts/summary") {
      if (unavailable) return json({ error: "unavailable" }, 503);
      return json({ available: true, enabled, new_count: alerts.filter(a => !a.acknowledged_at && !a.ignored).length, total: alerts.length, last_checked_at: 1791417660, dropped_lookups: 0, removed_alerts: 0, collection: { state: stale ? "unavailable" : "active", last_success: null }, sources: ["adult", "phishing", "malware", "fraud", "gambling"].map(category => ({ category, label: category, source: "Block List Project", url: "https://example.org/list", entries: 1000, updated_at: 1791417660, attempted_at: 1791417660, stale, updating: false })) });
    }
    if (path === "/api/v1/dns-activity/alerts") {
      if (unavailable) return json({ error: "unavailable" }, 503);
      const filtered = alerts.filter(a => (url.searchParams.get("view") === "all" || (!a.ignored && !a.acknowledged_at)) && (!url.searchParams.get("category") || a.category === url.searchParams.get("category")));
      const offset = Number(url.searchParams.get("offset")), limit = Number(url.searchParams.get("limit"));
      return json({ alerts: enabled ? filtered.slice(offset, offset + limit) : [], total: enabled ? filtered.length : 0, offset, limit });
    }
    if (path === "/api/v1/dns-activity/alerts/exceptions") return json({ exceptions });
    const action = path.match(/alerts\/(\d+)\/(acknowledge|ignore)$/);
    if (action) {
      const alert = alerts.find(a => a.id === Number(action[1]))!;
      if (action[2] === "acknowledge") alert.acknowledged_at = 1791417660;
      else { alert.ignored = true; exceptions.push({ id: alert.id, domain: alert.domain, category: alert.category }); }
      return json({ updated: true });
    }
    const remove = path.match(/exceptions\/(\d+)$/);
    if (remove) { const id = Number(remove[1]); exceptions = exceptions.filter(a => a.id !== id); alerts.forEach(a => { if (a.id === id) a.ignored = false; }); return json({ removed: true }); }
    if (path === "/api/v1/dns-activity/clear") { alerts = []; return json({ cleared: true }); }
    if (path === "/api/v1/dns-activity/alerts/refresh") return json({ queued: true }, 202);
    if (path === "/api/v1/dns-activity/recent") return json({ available: true, enabled, entries: [] });
    if (path === "/api/v1/dns-activity") return json({ available: true, enabled, collection: { state: "active" }, period: url.searchParams.get("period"), total_lookups: 0, site_count: 0, points: [], sites: [], flagged: [], devices: [{ address: "192.0.2.1", lookups: 0, sites: 0, last_seen: 0 }, { address: "192.0.2.2", lookups: 0, sites: 0, last_seen: 0 }].filter(a => !url.searchParams.get("device") || a.address === url.searchParams.get("device")) });
    return json(responses[path] ?? {});
  });
  return { mutations, fail: () => { unavailable = true; }, stale: () => { stale = true; } };
}

test("sidebar services distinguish enabled configuration from measured runtime", async ({ page }) => {
  await fixture(page);
  const config = structuredClone(CONFIG);
  config.wifi.enabled = true; config.squid_proxy.enabled = true; config.cloudflare.ddns_enabled = true;
  await page.route("**/api/v1/config", route => route.fulfill({ contentType: "application/json", body: JSON.stringify(config) }));
  await page.route("**/api/v1/system", route => route.fulfill({ contentType: "application/json", body: JSON.stringify({ ...SYSTEM, runtime: { available: true, wan_connected: false, ddns: { running: true } } }) }));
  await page.goto("/#network");
  await expect(page.locator("#network .subpage-hero-head")).toContainText("WAN Disconnected");
  await expect(page.locator("#network .subpage-hero-facts")).toContainText("plain upstream DNS");
  await page.goto("/#cloudflare");
  await expect(page.locator("#cloudflare .subpage-hero-head")).toContainText("Service running");
  await expect(page.locator("#cloudflare .subpage-hero-head")).not.toContainText("In sync");
  await page.goto("/#wifi");
  await expect(page.locator("#wifi .subpage-hero-head")).toContainText("Wi-Fi Enabled");
  await page.goto("/#squid");
  await expect(page.locator("#squid .subpage-hero-facts")).toContainText("runtime status not measured here");
  await expect(page.locator("#squid .subpage-hero-facts")).not.toContainText("Running");
});

for (const design of ["noema", "studio"]) for (const dark of [false, true]) {
  test(`${design} ${dark ? "dark" : "light"}: network alerts, actions, pagination and retained device options`, async ({ page }) => {
    await page.addInitScript(({ design, dark }) => { localStorage.setItem("minimalrouter:design", design); localStorage.setItem("minimalrouter:theme", dark ? "dark" : "light"); }, { design, dark });
    const state = await fixture(page);
    const errors: string[] = []; page.on("pageerror", e => errors.push(e.message));
    await page.goto("/#security");
    await expect(page.getByLabel("DNS risk overview")).toContainText("27 new DNS risk alerts");
    await page.getByRole("button", { name: "Open DNS alerts" }).click();
    const panel = page.getByRole("article", { name: "DNS risk alerts" });
    await expect(panel).toContainText("does not prove someone visited");
    await expect(panel.locator(".dns-risk-alerts > li")).toHaveCount(25);
    await panel.getByRole("button", { name: "Next alerts" }).click();
    await expect(panel.locator(".dns-risk-alerts > li")).toHaveCount(2);
    await panel.getByRole("button", { name: "Previous alerts" }).click();
    await page.getByLabel("Alert category").selectOption("adult");
    await expect(panel.locator(".dns-risk-alerts > li")).toHaveCount(1);
    await panel.getByRole("button", { name: "Mark reviewed", exact: true }).click();
    await expect(panel).toContainText("No new matching alerts");
    await page.getByLabel("Alert view").selectOption("all");
    await expect(panel.locator(".dns-risk-alerts")).toContainText("Reviewed");
    page.on("dialog", dialog => dialog.accept());
    await panel.getByRole("button", { name: "Ignore domain/category" }).click();
    await expect(panel).toContainText("Ignored by exception");
    await panel.getByText("Category lists & exceptions", { exact: true }).click();
    await expect(panel.locator(".dns-risk-exceptions")).toContainText("risk-1.example");
    await panel.getByRole("button", { name: "Remove exception" }).click();
    await expect(panel).toContainText("No exceptions.");
    await panel.getByRole("button", { name: "Update category lists" }).click();
    await expect(panel).toContainText("List update queued");
    await page.getByLabel("Device", { exact: true }).selectOption("192.0.2.1");
    await expect(page.getByLabel("Device", { exact: true }).locator("option")).toHaveCount(3);
    await page.getByLabel("Device", { exact: true }).selectOption("192.0.2.2");
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
    await page.screenshot({ path: test.info().outputPath("dns-alerts.png"), fullPage: true });
    await page.getByRole("button", { name: "Delete history", exact: true }).click();
    await expect(panel).toContainText("No matching alerts");
    expect(state.mutations.every(path => path.includes("/dns-activity/"))).toBe(true);
    expect(errors).toEqual([]);
  });
}

test("stale sources and failed monitoring never report safe traffic or retain old alert counts", async ({ page }) => {
  await page.clock.install();
  const state = await fixture(page); await page.goto("/#dns-activity");
  const panel = page.getByRole("article", { name: "DNS risk alerts" });
  await expect(panel).toContainText("Monitoring");
  state.stale(); await page.clock.fastForward(61000);
  await expect(panel).toContainText("Coverage is incomplete.");
  state.fail(); await page.clock.fastForward(61000);
  await expect(panel.getByRole("alert")).toBeVisible();
  await expect(panel.locator(".dns-risk-alerts > li")).toHaveCount(0);
  await expect(page.locator(".dns-risk-nav-count")).toHaveCount(0);
});

test("disabling recording hides risk history and leaves exception controls accessible", async ({ page }) => {
  await fixture(page); await page.goto("/#dns-activity");
  const panel = page.getByRole("article", { name: "DNS risk alerts" });
  await expect(panel.locator(".dns-risk-alerts > li")).toHaveCount(25);
  page.on("dialog", dialog => dialog.accept());
  await page.getByLabel("Record DNS activity", { exact: true }).click();
  await expect(page.getByLabel("Record DNS activity", { exact: true })).not.toBeChecked();
  await expect(panel).toContainText("Recording off");
  await expect(panel.locator(".dns-risk-alerts > li")).toHaveCount(0);
  await panel.getByText("Category lists & exceptions", { exact: true }).click();
  await expect(panel.getByRole("button", { name: "Update category lists" })).toBeDisabled();
});
