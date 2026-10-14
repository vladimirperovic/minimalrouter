import { test, expect, type Page } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";
import { networkFixture } from "./fixtures/network";

// Regressions from the v0.3.1 live test on Hyper-V (private findings M3, L1).

async function router(page: Page) {
  const config = structuredClone(CONFIG);
  const puts: any[] = [];
  await page.route("**/api/v1/**", async route => {
    const request = route.request(), path = new URL(request.url()).pathname;
    let body: any = {};
    if (path === "/api/v1/auth/session") body = { authenticated: true, csrf_token: "test" };
    else if (path === "/api/v1/config/preview") body = { changes: ["Trusted management networks"], risk: "high", requires_confirmation: true, expected_interruption: "Management connectivity may change." };
    else if (path === "/api/v1/config") {
      if (request.method() === "PUT") { const next = request.postDataJSON(); puts.push(next); Object.assign(config, next); config.revision++; body = { id: "test", state: "Committed" }; }
      else body = { ...config, wireguard: { ...config.wireguard, private_key: "[REDACTED]" } };
    } else if (path === "/api/v1/system") body = SYSTEM;
    else if (path === "/api/v1/health") body = HEALTH;
    else if (path === "/api/v1/gateway/summary") body = GW_SUMMARY;
    else if (path === "/api/v1/gateway/settings") body = GW_SETTINGS;
    else if (path === "/api/v1/snapshots") body = [];
    else if (path === "/api/v1/audit/events") body = { events: [] };
    const network = networkFixture(path, config); if (network) body = network;
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });
  return { puts };
}

test("an untrusted WireGuard network is called out and can be trusted after confirmation", async ({ page }) => {
  const r = await router(page);
  await page.goto("/#security");
  const notice = page.locator(".trusted-networks-notice");
  await expect(notice).toContainText("10.8.0.0/24");
  await notice.getByRole("button", { name: "Trust WireGuard network" }).click();

  // L1: the in-page confirmation is centred, not pinned to the top-left corner.
  const dialog = page.locator("dialog[data-mr-confirm][open]");
  await expect(dialog).toBeVisible();
  const box = await dialog.boundingBox();
  const viewport = page.viewportSize()!;
  expect(box).not.toBeNull();
  expect(Math.abs(box!.x + box!.width / 2 - viewport.width / 2)).toBeLessThan(4);
  expect(Math.abs(box!.y + box!.height / 2 - viewport.height / 2)).toBeLessThan(4);

  await dialog.locator("[data-mr-confirm-accept]").click();
  await expect.poll(() => r.puts.length).toBe(1);
  expect(r.puts[0].trusted_networks).toEqual(["192.168.1.0/24", "10.8.0.0/24"]);
  await expect(notice).toHaveCount(0);
});
