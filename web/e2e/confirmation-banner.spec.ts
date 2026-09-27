import { test, expect } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";

// At the confirmation deadline the router rolls a provisional change back on
// its own. The dashboard must stop offering to confirm that transaction rather
// than keep a "0s" banner whose button confirms nothing.
test("the confirmation banner clears itself after the router's automatic rollback", async ({ page }) => {
  let rolledBack = false;
  const deadline = new Date(Date.now() + 4_000).toISOString();
  await page.addInitScript(() => localStorage.setItem("minimalrouter:wan-speed-estimate-attempt", String(Date.now())));
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/v1/transactions/pending") {
      return route.fulfill({ json: rolledBack ? { pending: false }
        : { pending: true, id: "tx-provisional", state: "AwaitingConfirmation", confirmation_deadline: deadline } });
    }
    const responses: Record<string, unknown> = {
      "/api/v1/auth/session": { authenticated: true, csrf_token: "test" }, "/api/v1/config": CONFIG,
      "/api/v1/system": SYSTEM, "/api/v1/health": HEALTH, "/api/v1/gateway/summary": GW_SUMMARY,
      "/api/v1/gateway/settings": GW_SETTINGS, "/api/v1/gateway/history": { points: [] }, "/api/v1/snapshots": [],
      "/api/v1/devices/pauses": { pauses: [] }, "/api/v1/audit/events": { events: [] },
    };
    return route.fulfill({ json: responses[path] ?? {} });
  });
  await page.goto("/");
  const banner = page.getByText(/awaiting confirmation/i);
  await expect(banner).toBeVisible();
  rolledBack = true; // the router's deadline passes while the page stays open
  await expect(banner).toBeHidden({ timeout: 15_000 });
});
