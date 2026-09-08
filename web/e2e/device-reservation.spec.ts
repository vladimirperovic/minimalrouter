import { expect, test } from "@playwright/test";
import type { RouterConfig } from "../src/api-types";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";

test("a reservation saves the edited device name and cancels discard name changes", async ({ page }) => {
  let config = structuredClone(CONFIG) as RouterConfig;
  let saved: RouterConfig | undefined;
  const lease = { hostname: "test-air", mac: "02:00:00:00:00:14", ip_address: "192.168.1.150", expires_at: Math.floor(Date.now() / 1000) + 3600 };
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    let body: unknown = {};
    if (path === "/api/v1/auth/session") body = { authenticated: true, csrf_token: "test" };
    else if (path === "/api/v1/config/preview") body = { changes: ["Update DHCP reservation"], risk: "low", requires_confirmation: false };
    else if (path === "/api/v1/config") {
      if (route.request().method() === "PUT") {
        saved = route.request().postDataJSON() as RouterConfig;
        config = { ...saved, revision: saved.revision + 1 };
        body = { state: "confirmed", revision: config.revision };
      } else body = config;
    } else if (path === "/api/v1/system") body = { ...SYSTEM, runtime: { ...SYSTEM.runtime, dhcp_leases: [lease] } };
    else if (path === "/api/v1/health") body = HEALTH;
    else if (path === "/api/v1/gateway/summary") body = GW_SUMMARY;
    else if (path === "/api/v1/gateway/settings") body = GW_SETTINGS;
    else if (path === "/api/v1/snapshots") body = [];
    else if (path === "/api/v1/devices/pauses") body = { pauses: [] };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  page.on("dialog", (dialog) => dialog.accept());
  await page.goto("/#network");
  const reserve = page.getByRole("button", { name: "Reserve an IP address for test-air" });
  await reserve.click();
  const dialog = page.getByRole("dialog", { name: "Reserve an IP for test-air" });
  await expect(dialog.getByLabel("Device name")).toHaveValue("test-air");
  await dialog.getByLabel("Device name").fill("discarded-name");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await reserve.click();
  await expect(dialog.getByLabel("Device name")).toHaveValue("test-air");
  await dialog.getByLabel("Device name").fill("  office-mac  ");
  await dialog.getByLabel("Reserved IPv4 address").fill("192.168.1.14");
  await expect(dialog.getByLabel("MAC address")).toHaveValue(lease.mac);
  await expect(dialog.getByLabel("MAC address")).toHaveAttribute("readonly", "");
  await dialog.getByRole("button", { name: "Reserve IP", exact: true }).click();
  await expect.poll(() => saved?.dhcp.static_leases?.[0]).toMatchObject({ hostname: "office-mac", mac: lease.mac, ip_address: "192.168.1.14" });
  await expect(dialog).toBeHidden();
});
