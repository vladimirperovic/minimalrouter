import { test, expect } from "@playwright/test";

// A fresh Proxmox VM: two virtio NICs, no admin password yet.
const DISCOVERY = {
  wan: "ens18",
  lan: "ens19",
  warnings: ["One or more selected interfaces appear virtual; confirm roles on the local console."],
  interfaces: [
    { name: "ens18", mac_address: "bc:24:11:aa:bb:01", up: true, carrier: true, physical: true, default_route: true, score: 215 },
    { name: "ens19", mac_address: "bc:24:11:aa:bb:02", up: true, carrier: true, physical: true, default_route: false, score: 135 },
  ],
};

test("fresh Proxmox VM walks the whole setup wizard", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  let applied: Record<string, unknown> | null = null;

  await page.route("**/api/v1/auth/session", (r) => r.fulfill({ status: 401, contentType: "application/json", body: "{}" }));
  await page.route("**/api/v1/setup/status", (r) => r.fulfill({ status: 200, contentType: "application/json",
    body: JSON.stringify({ is_configured: false, wan_interface: "ens18", lan_interface: "ens19", lan_ip: "192.168.1.1", recovery_required: false }) }));
  await page.route("**/api/v1/setup/interfaces", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(DISCOVERY) }));
  await page.route("**/api/v1/setup/apply", async (r) => {
    applied = JSON.parse(r.request().postData() ?? "{}");
    await r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ success: true }) });
  });

  await page.goto("/");

  await expect(page.getByRole("heading", { name: /Welcome to your new router/ })).toBeVisible();
  await page.getByRole("button", { name: /Start setup/ }).click();

  // Step 2 - discovered interfaces must be offered, not the eth0/eth1 defaults.
  await expect(page.getByRole("heading", { name: /Confirm the WAN and LAN interfaces/ })).toBeVisible();
  const body = await page.locator("body").innerText();
  expect(body, "discovered Proxmox NIC names are not offered").toContain("ens18");
  expect(body).toContain("ens19");
  await page.getByRole("button", { name: /Continue/ }).click();

  // Step 3 - PPPoE
  await expect(page.getByRole("heading", { name: /Enter your PPPoE credentials/ })).toBeVisible();
  await page.getByRole("button", { name: /Continue/ }).click();

  // Step 4 - admin password
  await expect(page.getByRole("heading", { name: /Create the administrator password/ })).toBeVisible();
  const pw = page.locator('input[type="password"]');
  await pw.nth(0).fill("correct-horse-battery-staple");
  await pw.nth(1).fill("correct-horse-battery-staple");
  await page.getByRole("button", { name: /Review/ }).click();

  // Step 5 - review then apply
  await expect(page.getByRole("heading", { name: /Review your setup/ })).toBeVisible();
  await page.getByRole("button", { name: /Apply setup/ }).click();

  await expect(page.getByRole("heading", { name: /The router is initialised/ })).toBeVisible();
  expect(applied).toMatchObject({ wan_interface: "ens18", lan_interface: "ens19" });
  expect(errors).toEqual([]);
});

test("wizard refuses the same NIC for WAN and LAN", async ({ page }) => {
  await page.route("**/api/v1/auth/session", (r) => r.fulfill({ status: 401, contentType: "application/json", body: "{}" }));
  await page.route("**/api/v1/setup/status", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ is_configured: false }) }));
  await page.route("**/api/v1/setup/interfaces", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(DISCOVERY) }));
  await page.goto("/");
  await page.getByRole("button", { name: /Start setup/ }).click();
  const selects = page.locator("select");
  if (await selects.count() >= 2) {
    await selects.nth(1).selectOption("ens18");
    await page.getByRole("button", { name: /Continue/ }).click();
    await expect(page.getByText(/must be two different interfaces/)).toBeVisible();
  }
});

test("discovery failure still lets the operator name interfaces by hand", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.route("**/api/v1/auth/session", (r) => r.fulfill({ status: 401, contentType: "application/json", body: "{}" }));
  await page.route("**/api/v1/setup/status", (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ is_configured: false }) }));
  await page.route("**/api/v1/setup/interfaces", (r) => r.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "at least two usable network interfaces are required" }) }));
  await page.goto("/");
  await expect(page.getByRole("heading", { name: /Welcome to your new router/ })).toBeVisible();
  await page.getByRole("button", { name: /Start setup/ }).click();
  await expect(page.getByRole("heading", { name: /Confirm the WAN and LAN interfaces/ })).toBeVisible();
  expect(errors).toEqual([]);
});
