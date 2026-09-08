import { expect, test, type Page } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";

async function setup(page: Page, enabled = true) {
  await page.addInitScript(() => localStorage.setItem("minimalrouter:wan-speed-estimate-attempt", String(Date.now())));
  // The browser deliberately has a different clock from the appliance.
  const now = Date.parse("2026-10-01T00:02:00Z") / 1000;
  const names = ["active-laptop", "sleeping-laptop", "lease-only"];
  const leases = names.map((hostname, i) => ({ hostname, mac: `02:00:00:00:00:0${i}`, ip_address: `192.168.1.${100 + i}`, expires_at: now + 86400 }));
  const usage = (address: string, last_seen_epoch: number, hostname?: string) => ({ address, last_seen_epoch, hostname, rx_bytes: 100, tx_bytes: 50, total_bytes: 150 });
  const state = { fail: false, empty: false, writes: 0 };
  await page.route("**/api/v1/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() !== "GET") state.writes++;
    if (path === "/api/v1/accounting" && state.fail) return route.fulfill({ status: 503, body: "Unavailable" });
    const data: Record<string, unknown> = {
      "/api/v1/auth/session": { authenticated: true, csrf_token: "test" },
      "/api/v1/config": { ...CONFIG, accounting: { enabled, retention_months: 13 } },
      "/api/v1/system": { ...SYSTEM, runtime: { ...SYSTEM.runtime, dhcp_leases: leases } },
      "/api/v1/health": HEALTH, "/api/v1/gateway/summary": GW_SUMMARY,
      "/api/v1/gateway/settings": GW_SETTINGS, "/api/v1/snapshots": [],
      "/api/v1/devices/pauses": { pauses: [] },
      "/api/v1/accounting": { available: true, enabled, updated_at: new Date(now * 1000).toISOString(), months: state.empty ? [] : [
        { month: "2026-10", devices: [usage(leases[0].ip_address, now - 60), usage(leases[1].ip_address, now - 3600)] },
        { month: "2026-09", devices: [{ ...usage("192.168.1.200", now - 300, "static-sensor"), mac: leases[2].mac }] },
      ] },
    };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(data[path] ?? {}) });
  });
  return state;
}

for (const design of ["noema", "studio"]) {
  test(`${design}: Overview shows recent activity and links to the full known list`, async ({ page }) => {
    await page.addInitScript(design => {
      localStorage.setItem("minimalrouter:design", design);
      localStorage.setItem("minimalrouter:wan-speed-estimate-attempt", String(Date.now()));
    }, design);
    const state = await setup(page);
    await page.goto("/#overview");
    const section = page.locator("#overview-devices");
    await expect(section.getByRole("heading", { name: "Active devices" })).toBeVisible();
    await expect(section.locator(".device-hostname")).toHaveText(["active-laptop", "static-sensor"]);
    await expect(section.locator(design === "studio" ? ".device-last-seen" : ".elegant-cell-expires")).toHaveText(["1 min ago", "5 min ago"]);
    if (design === "noema") {
    await expect(section.locator("thead")).toContainText("Last seen");
    await section.getByPlaceholder("Search name, IP or MAC").fill("sleeping");
    await expect(section.getByText("No devices match your search.")).toBeVisible();
    }
    await section.getByRole("link", { name: "View all devices" }).click();
    await expect(page.getByRole("heading", { name: "Known devices", exact: true })).toBeVisible();
    await expect(page.locator(".modern-device-section .device-hostname")).toHaveText(["active-laptop", "sleeping-laptop", "lease-only"]);
    expect(state.writes).toBe(0);
  });
}

test("disabled accounting and failures are not presented as zero connected devices", async ({ page }) => {
  const state = await setup(page, false);
  await page.goto("/#overview");
  const section = page.locator("#overview-devices");
  await expect(section.getByText("Device activity is unavailable while traffic accounting is off.")).toBeVisible();
  await expect(section.locator(".device-hostname")).toHaveCount(0);
  await section.getByRole("link", { name: "View all devices" }).click();
  await expect(page.locator(".modern-device-section .device-hostname")).toHaveCount(3);
  expect(state.writes).toBe(0);
});

test("activity clears on failed refresh and recovers, with a separate measured-empty state", async ({ page }) => {
  const state = await setup(page);
  await page.clock.install();
  await page.goto("/#overview");
  const section = page.locator("#overview-devices");
  await expect(section.locator(".device-hostname")).toHaveCount(2);
  state.fail = true;
  await page.clock.runFor(31000);
  await expect(section.getByText("Device activity is temporarily unavailable.")).toBeVisible();
  await expect(section.locator(".device-hostname")).toHaveCount(0);
  state.fail = false;
  await page.clock.runFor(31000);
  await expect(section.locator(".device-hostname")).toHaveCount(2);
  state.empty = true;
  await page.clock.runFor(31000);
  await expect(section.getByText("No device traffic recorded in the last 10 minutes.")).toBeVisible();
  await expect(section.locator(".modern-device-count")).toHaveText("0 active");
  expect(state.writes).toBe(0);
});
