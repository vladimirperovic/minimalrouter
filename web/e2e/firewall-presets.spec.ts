import { test, expect, type Page } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";

// A suggested rule is switched on by ticking it and pressing "Turn on". The
// generic themed input rule once gave these bare checkboxes a 42px minimum
// height and side padding, which left them zero pixels wide: nothing could be
// ticked, so every "Turn on" stayed disabled in both designs.
async function routerFixture(page: Page) {
  const config = structuredClone(CONFIG);
  const writes: Array<{ path: string; body: unknown }> = [];
  await page.addInitScript(() => localStorage.setItem("minimalrouter:wan-speed-estimate-attempt", String(Date.now())));
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === "POST" && path === "/api/v1/config/preview") {
      return route.fulfill({ json: { changes: ["Firewall"], risk: "low", requires_confirmation: false } });
    }
    if (request.method() === "PUT" && path === "/api/v1/config") {
      const body = request.postDataJSON();
      writes.push({ path, body });
      Object.assign(config, body, { revision: config.revision + 1 });
      return route.fulfill({ json: { id: "tx-preset", state: "Committed" } });
    }
    const responses: Record<string, unknown> = {
      "/api/v1/auth/session": { authenticated: true, csrf_token: "test" }, "/api/v1/config": config,
      "/api/v1/system": SYSTEM, "/api/v1/health": HEALTH, "/api/v1/gateway/summary": GW_SUMMARY,
      "/api/v1/gateway/settings": GW_SETTINGS, "/api/v1/gateway/history": { points: [] }, "/api/v1/snapshots": [],
      "/api/v1/devices/pauses": { pauses: [] }, "/api/v1/audit/events": { events: [] },
    };
    return route.fulfill({ json: responses[path] ?? {} });
  });
  return writes;
}

for (const design of ["noema", "studio"]) {
  test(`${design}: a suggested firewall rule can be ticked and turned on`, async ({ page, isMobile }) => {
    test.skip(isMobile, "desktop table layout");
    await page.addInitScript((value) => localStorage.setItem("minimalrouter:design", value), design);
    const writes = await routerFixture(page);
    page.on("dialog", (dialog) => void dialog.accept());
    await page.goto("/#firewall");

    const tick = page.getByRole("checkbox", { name: "Select Block Telnet" });
    const box = (await tick.boundingBox())!;
    expect(box.width).toBeGreaterThanOrEqual(14);
    expect(box.height).toBeLessThanOrEqual(24);

    const row = page.getByRole("row").filter({ has: tick });
    await expect(row.getByRole("button", { name: "Turn on" })).toBeDisabled();
    await tick.check();
    await row.getByRole("button", { name: "Turn on" }).click();
    await expect.poll(() => writes.length).toBe(1);
    const rules = (writes[0].body as { firewall: { custom_rules: Array<{ protocol: string; dst_port: number; action: string }> } }).firewall.custom_rules;
    expect(rules).toContainEqual(expect.objectContaining({ protocol: "tcp", dst_port: 23, action: "deny" }));
  });
}
