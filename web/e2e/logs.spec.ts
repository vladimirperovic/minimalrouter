import { expect, test, type Page } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";
import type { BootSample } from "../src/lib/logs";

type Event = { id: string; event_type: string; category: string; actor: string; timestamp: string; details: Record<string, string> | null };
const event = (id: string, category = "security", details: Record<string, string> | null = null): Event => ({ id, category, event_type: `${category}.${id}`, actor: "192.0.2.10", timestamp: "2026-10-09T08:00:00Z", details });
const BOOT = { id: "test-boot", started_at: "2026-10-09T08:00:00Z", completed: false, status: "capturing", expected: ["management", "pppoe", "dns", "internet"], readiness: { management_seconds: 2 }, events: null, samples: null };

async function fixture(page: Page, options: { startupFailed?: boolean; events?: Event[]; complete?: boolean; samples?: BootSample[] } = {}) {
  const state = { requests: [] as string[], errors: [] as string[], startupFailed: !!options.startupFailed, events: options.events ?? [event("nil")], boot: { ...BOOT, samples: options.samples ?? null, ...(options.complete ? { completed: true, status: "timeout" } : {}) } };
  page.on("pageerror", error => state.errors.push(error.message));
  await page.addInitScript(() => localStorage.setItem("minimalrouter:wan-speed-estimate-attempt", String(Date.now())));
  await page.route("**/api/v1/**", async route => {
    const url = new URL(route.request().url()), path = url.pathname;
    state.requests.push(path + url.search);
    const data: Record<string, unknown> = {
      "/api/v1/auth/session": { authenticated: true, csrf_token: "test" },
      "/api/v1/config": CONFIG, "/api/v1/system": SYSTEM, "/api/v1/health": HEALTH,
      "/api/v1/gateway/summary": GW_SUMMARY, "/api/v1/gateway/settings": GW_SETTINGS,
      "/api/v1/snapshots": [], "/api/v1/devices/pauses": { pauses: [] },
      "/api/v1/startup/boots": { boots: [state.boot] },
      "/api/v1/startup/boots/test-boot": { boot: state.boot, status: state.boot.status },
    };
    if (path === "/api/v1/audit/events") {
      const category = url.searchParams.get("category"), q = url.searchParams.get("q");
      const matches = state.events.filter(item => (!category || item.category === category) && (!q || JSON.stringify(item).includes(q)));
      const offset = Number(url.searchParams.get("cursor") || 0), limit = Number(url.searchParams.get("limit") || 100);
      data[path] = { events: matches.slice(offset, offset + limit), matching_count: matches.length, retained_count: state.events.length,
        retention_limit: 5000, has_more: offset + limit < matches.length, next_cursor: String(offset + limit), generated_at: new Date().toISOString() };
    }
    await route.fulfill({ status: path === "/api/v1/startup/boots" && state.startupFailed ? 503 : 200, json: data[path] ?? {} });
  });
  await page.goto("/#logs");
  return state;
}

test("nullable data renders and capture status never claims success", async ({ page }) => {
  const state = await fixture(page);
  await expect(page.locator(".audit-table-scroll")).toContainText("security.nil");
  await expect(page.locator(".audit-table-scroll")).toContainText("Recorded");
  await expect(page.getByText("Startup capture in progress", { exact: true })).toBeVisible();
  await expect(page.getByText("Waiting for readiness").first()).toBeVisible();
  await expect(page.locator(".startup-metric").filter({ hasText: "Disk now" })).toContainText("25.0%");
  expect(state.errors).toEqual([]);
});

test("resource chart inspects real samples with pointer and keyboard and distinguishes missing RAM", async ({ page }) => {
  const state = await fixture(page, { samples: [
    { offset_seconds: 0, cpu_percent: 4, memory_used_mb: 160, memory_total_mb: 512 },
    { offset_seconds: 2, cpu_percent: 37, memory_used_mb: 0, memory_total_mb: 0 },
    { offset_seconds: 7, cpu_percent: 12, memory_used_mb: 174, memory_total_mb: 512 },
  ] });
  const plot = page.getByRole("slider", { name: "Startup resource sample" });
  await expect(plot).toHaveAttribute("aria-valuetext", "At +7s, CPU 12.0%, RAM 34.0% (174 of 512 MB)");
  await plot.press("Home");
  await expect(plot).toHaveAttribute("aria-valuetext", "At +0s, CPU 4.0%, RAM 31.3% (160 of 512 MB)");
  await plot.press("ArrowRight");
  await expect(plot).toHaveAttribute("aria-valuetext", "At +2s, CPU 37.0%, RAM Unavailable");
  await expect(page.locator(".startup-reading.is-memory")).toContainText("Unavailable");
  await plot.press("End");
  await expect(plot).toHaveAttribute("aria-valuenow", "2");
  await plot.click({ position: { x: 45, y: 100 } });
  await expect(plot).toHaveAttribute("aria-valuenow", "0");
  await expect(page.locator(".startup-metric").filter({ hasText: "Peak CPU" })).toContainText("37.0%");
  expect(state.errors).toEqual([]);
});

test("a single resource sample at boot zero stays readable and keyboard selection stays bounded", async ({ page }) => {
  await fixture(page, { samples: [{ offset_seconds: 0, cpu_percent: 0, memory_used_mb: 0, memory_total_mb: 512 }] });
  const plot = page.getByRole("slider", { name: "Startup resource sample" });
  await expect(plot).toHaveAttribute("aria-valuetext", "At +0s, CPU 0.0%, RAM 0.0% (0 of 512 MB)");
  await plot.press("ArrowRight");
  await plot.press("ArrowLeft");
  await expect(plot).toHaveAttribute("aria-valuenow", "0");
  expect(await plot.locator("svg").innerHTML()).not.toMatch(/NaN|Infinity/);
});

test("timeout is explicit and startup loading can be retried after first failure", async ({ page }) => {
  const state = await fixture(page, { startupFailed: true, complete: true });
  const panel = page.locator(".startup-timeline");
  await expect(panel).toContainText("Startup timeline unavailable (503)");
  state.startupFailed = false;
  await panel.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(panel).toContainText("Capture timed out — readiness incomplete");
  await expect(panel).not.toContainText("Boot completed");
  expect(state.errors).toEqual([]);
});

test("manual refresh fetches fresh audit and startup data immediately", async ({ page }) => {
  const state = await fixture(page);
  await expect(page.locator(".audit-table-scroll")).toContainText("security.nil");
  const count = state.requests.filter(path => path === "/api/v1/startup/boots").length;
  state.events = [event("fresh")];
  await page.locator(".subpage-hero-head").getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.locator(".audit-table-scroll")).toContainText("security.fresh");
  await expect.poll(() => state.requests.filter(path => path === "/api/v1/startup/boots").length).toBeGreaterThan(count);
});

test("server filters and pagination reach older events and export their scope", async ({ page }) => {
  const events = Array.from({ length: 205 }, (_, index) => event(`row${index}`, index === 204 ? "recovery" : "security", { ninth: index === 204 ? "needle" : "value" }));
  await fixture(page, { events });
  await expect(page.locator(".audit-table-scroll tbody tr")).toHaveCount(100);
  await page.getByRole("button", { name: "Older events" }).click();
  await expect(page.locator(".audit-table-scroll")).toContainText("security.row100");
  await page.getByRole("button", { name: "Older events" }).click();
  await expect(page.locator(".audit-table-scroll tbody tr")).toHaveCount(5);
  await page.getByRole("button", { name: "recovery", exact: true }).click();
  await expect(page.locator(".audit-table-scroll tbody tr")).toHaveCount(1);
  await page.getByLabel("Search audit events").fill("needle");
  await expect.poll(async () => await page.locator(".audit-table-scroll").textContent()).toContain("recovery.row204");
  await expect(page.locator(".logs-pagination")).toContainText("Page 1");
  const downloaded = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export diagnostics", exact: true }).click();
  const stream = await (await downloaded).createReadStream(), chunks: Buffer[] = [];
  for await (const chunk of stream!) chunks.push(Buffer.from(chunk));
  const exported = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  expect(exported.scope).toContain("All retained audit records matching");
  expect(exported.events).toHaveLength(1);
  expect(exported.startup.selected_boot.id).toBe("test-boot");
  expect(exported.startup.storage_now.usage_percent).toBe(25);
});

test("diagnostic export traverses all matching pages", async ({ page }) => {
  await fixture(page, { events: Array.from({ length: 605 }, (_, index) => event(`row${index}`)) });
  await expect(page.locator(".audit-table-scroll tbody tr")).toHaveCount(100);
  const downloaded = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export diagnostics", exact: true }).click();
  const stream = await (await downloaded).createReadStream(), chunks: Buffer[] = [];
  for await (const chunk of stream!) chunks.push(Buffer.from(chunk));
  const exported = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  expect(exported.events).toHaveLength(605);
  expect(exported.history_changed_during_export).toBe(false);
  expect(exported.export_until).toBeTruthy();
});

test("initial search debounce cannot reset an already opened history page", async ({ page }) => {
  await page.clock.install();
  await fixture(page, { events: Array.from({ length: 205 }, (_, index) => event(`row${index}`)) });
  await expect(page.locator(".audit-table-scroll tbody tr")).toHaveCount(100);
  await page.getByRole("button", { name: "Older events" }).click();
  await expect(page.locator(".audit-table-scroll")).toContainText("security.row100");
  await page.clock.runFor(500);
  await expect(page.locator(".logs-pagination")).toContainText("Page 2");
  await expect(page.locator(".audit-table-scroll")).toContainText("security.row100");
});

test("hidden Logs stop polling and leaving the route aborts its work", async ({ page }) => {
  await page.clock.install();
  const state = await fixture(page);
  await expect(page.locator(".audit-table-scroll")).toContainText("security.nil");
  const logRequests = () => state.requests.filter(path => path.startsWith("/api/v1/audit/") || path.startsWith("/api/v1/startup/"));
  await page.evaluate(() => { Object.defineProperty(document, "hidden", { configurable: true, get: () => true }); document.dispatchEvent(new Event("visibilitychange")); });
  const hiddenCount = logRequests().length;
  await page.clock.runFor(65_000);
  expect(logRequests()).toHaveLength(hiddenCount);
  await page.evaluate(() => { Object.defineProperty(document, "hidden", { configurable: true, get: () => false }); document.dispatchEvent(new Event("visibilitychange")); });
  await expect.poll(() => logRequests().length).toBeGreaterThan(hiddenCount);
  await page.goto("/#firewall");
  const count = logRequests().length;
  await page.clock.runFor(65_000);
  expect(logRequests()).toHaveLength(count);
});

test("superseded searches abort and cannot replace the latest results", async ({ page }) => {
  const state = await fixture(page);
  await expect(page.locator(".audit-table-scroll")).toContainText("security.nil");
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let started = false;
  const failed: string[] = [];
  page.on("requestfailed", request => failed.push(request.url()));
  await page.route("**/api/v1/audit/events?**", async route => {
    const q = new URL(route.request().url()).searchParams.get("q");
    if (q === "slow") { started = true; await gate; await route.fulfill({ json: { events: [event("obsolete")] } }).catch(() => {}); }
    else if (q === "fast") await route.fulfill({ json: { events: [event("latest")] } });
    else await route.fallback();
  });
  await page.getByLabel("Search audit events").fill("slow");
  await expect.poll(() => started).toBe(true);
  await page.getByLabel("Search audit events").fill("fast");
  await expect(page.locator(".audit-table-scroll")).toContainText("security.latest");
  await expect.poll(() => failed.some(url => url.includes("q=slow"))).toBe(true);
  release();
  await expect(page.locator(".audit-table-scroll")).not.toContainText("obsolete");
  expect(state.errors).toEqual([]);
});

test("desktop and phone Logs keep scrolling inside their panels", async ({ page }) => {
  const state = await fixture(page, { events: Array.from({ length: 70 }, (_, index) => event(`row${index}`, "security", { path: "/api/v1/config", transaction_id: "tx-example" })) });
  await expect(page.locator(".audit-table-scroll tbody tr")).toHaveCount(70);
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
    await page.screenshot({ path: test.info().outputPath(`logs-${width}.png`), fullPage: true });
  }
  expect(state.errors).toEqual([]);
});
