import { test, expect, type Page } from "@playwright/test";
import type { SystemStatus } from "../src/api-types";
import type { FirmwareStatus, UpdateOperationState } from "../src/lib/updates";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";

async function router(page: Page, options: { auditFailed?: boolean; logoutFailed?: boolean; firmwareStatus?: FirmwareStatus; wgPreview?: { status?: number; server_key_configured?: boolean }; qos?: NonNullable<SystemStatus["runtime"]>["qos"] } = {}) {
  const config = structuredClone(CONFIG);
  const writes: Array<{ path: string; body: any }> = [];
  const wgPreview = { status: 200, server_key_configured: true as boolean | undefined, ...options.wgPreview };
  const firmware = { status: options.firmwareStatus, requests: 0 };
  let previews = 0;
  let documents = 0;
  page.on("request", request => { if (request.isNavigationRequest() && request.frame() === page.mainFrame()) documents++; });
  await page.route("**/api/v1/**", async route => {
    const request = route.request(), path = new URL(request.url()).pathname;
    let body: any = {}, status = 200;
    if (request.method() !== "GET") writes.push({ path, body: request.postData() ? request.postDataJSON() : undefined });
    if (path === "/api/v1/auth/session") body = { authenticated: true, csrf_token: "test" };
    else if (path === "/api/v1/auth/logout") { if (options.logoutFailed) { status = 503; body = { error: "unavailable" }; } }
    else if (path === "/api/v1/auth/change-password") { status = 400; body = { error: "test deliberately does not change credentials" }; }
    else if (path === "/api/v1/config/preview") body = { changes: ["Synthetic test change"], risk: "medium", requires_confirmation: false };
    else if (path === "/api/v1/config") {
      if (request.method() === "PUT") { Object.assign(config, request.postDataJSON()); config.revision++; body = { id: "test", state: "Committed" }; }
      else body = { ...config, wireguard: { ...config.wireguard, private_key: "[REDACTED]" } };
    } else if (path === "/api/v1/system") body = { ...SYSTEM, runtime: { ...SYSTEM.runtime, qos: options.qos } };
    else if (path === "/api/v1/health") body = HEALTH;
    else if (path === "/api/v1/gateway/summary") body = GW_SUMMARY;
    else if (path === "/api/v1/gateway/settings") body = GW_SETTINGS;
    else if (path === "/api/v1/snapshots") body = [];
    else if (path === "/api/v1/audit/events") { body = { events: [] }; if (options.auditFailed) status = 503; }
    else if (path === "/api/v1/wireguard/provisioning-preview") { previews++; status = wgPreview.status; body = { client_ip: "10.8.0.2", server_endpoint: "router.example.test:51820", server_key_configured: wgPreview.server_key_configured }; }
    else if (path === "/api/v1/firmware/status") { firmware.requests++; body = firmware.status ?? {}; }
    await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  });
  return { config, writes, wgPreview, firmware, previews: () => previews, documents: () => documents };
}

test("failed sign out stays honest and does not display a signed-out session", async ({ page }) => {
  await router(page, { logoutFailed: true }); await page.goto("/#network");
  await page.getByTitle("Account", { exact: true }).click(); await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByText(/Sign out failed \(503\)/)).toBeVisible();
  await expect(page.locator(".dashboard-app")).toBeVisible();
  await page.reload(); await expect(page.locator(".dashboard-app")).toBeVisible();
});

test("password change preserves every character and retains a failed form", async ({ page }) => {
  const r = await router(page); await page.goto("/#network");
  await page.getByTitle("Account", { exact: true }).click(); await page.getByRole("button", { name: "Change password", exact: true }).click();
  await page.locator('[name="old_password"]').fill(" old-password ");
  await page.locator('[name="new_password"]').fill(" new-password-123 ");
  await page.locator('[name="confirm_password"]').fill(" new-password-123 ");
  await page.getByRole("button", { name: "Update password", exact: true }).click();
  await expect.poll(() => r.writes.find(w => w.path.endsWith("change-password"))?.body).toEqual({ old_password: " old-password ", new_password: " new-password-123 " });
  await expect(page.locator('[name="old_password"]')).toBeVisible();
});

test("Enter saves DHCP, canonical refresh is immediate and unrelated WAN/DHCP drafts survive", async ({ page }) => {
  const r = await router(page); page.on("dialog", d => d.accept()); await page.goto("/#network");
  await page.locator('[name="pppoe_username"]').fill("unsaved-wan");
  await page.locator('[name="lease_time"]').fill("24h"); await page.locator('[name="lease_time"]').press("Enter");
  await expect.poll(() => r.config.dhcp.lease_time).toBe("24h"); expect(r.config.wan.username).toBe("isp");
  await expect(page.locator('[name="pppoe_username"]')).toHaveValue("unsaved-wan");
  await page.locator('[name="lease_time"]').fill("36h"); await page.getByRole("button", { name: "Save WAN", exact: true }).click();
  await expect.poll(() => r.config.wan.username).toBe("unsaved-wan");
  await expect(page.locator('[name="lease_time"]')).toHaveValue("36h");
  await expect(page.locator(".dashboard-loading")).toHaveCount(0); expect(r.config.dhcp.lease_time).toBe("24h");
});

test("audit failure is unknown activity, not zero incidents", async ({ page }) => {
  await router(page, { auditFailed: true }); await page.goto("/#security");
  await expect(page.getByRole("alert")).toContainText("Audit unavailable (503)");
  await expect(page.getByText("No security events recorded.", { exact: true })).toHaveCount(0);
  await expect(page.locator(".classic-security-command-facts")).toContainText("—");
});

test("WG detail modal contains Tab focus and restores its trigger", async ({ page }) => {
  const r = await router(page); (r.config.wireguard.peers as any[]).push({ id: "peer-test", name: "Test peer", public_key: "A".repeat(43) + "=", enabled: true, allowed_ips: ["10.8.0.2/32"] });
  await page.goto("/#wireguard"); const trigger = page.getByRole("button", { name: "More info", exact: true }); await trigger.click();
  const dialog = page.getByRole("dialog"); await expect(dialog).toBeVisible();
  for (let i = 0; i < 5; i++) { await page.keyboard.press("Tab"); await expect.poll(() => dialog.evaluate(e => e.contains(document.activeElement))).toBe(true); }
  await page.keyboard.press("Shift+Tab"); await expect.poll(() => dialog.evaluate(e => e.contains(document.activeElement))).toBe(true);
  await page.keyboard.press("Escape"); await expect(dialog).toHaveCount(0); await expect(trigger).toBeFocused();
});

test("provisioning preview polls only on the visible WG page", async ({ page, isMobile }) => {
  await page.clock.install(); const r = await router(page); await page.goto("/#network"); await expect(page.locator("#network")).toBeVisible();
  await page.clock.fastForward(16_000); expect(r.previews()).toBe(0);
  await openSection(page, isMobile, "#wireguard"); await expect.poll(r.previews).toBe(1);
  await page.evaluate(() => { Object.defineProperty(document, "hidden", { configurable: true, value: true }); document.dispatchEvent(new Event("visibilitychange")); });
  await page.clock.fastForward(31_000); expect(r.previews()).toBe(1);
  await page.evaluate(() => { Object.defineProperty(document, "hidden", { configurable: true, value: false }); document.dispatchEvent(new Event("visibilitychange")); });
  await expect.poll(r.previews).toBe(2);
});

test("Recovery renders once directly and after navigation, in both appearances", async ({ page, isMobile }) => {
  await router(page); await page.goto("/#recovery");
  for (let i = 0; i < 2; i++) {
    await expect(page.locator("#recovery .security-recovery-card")).toHaveCount(1);
    await expect(page.getByText("Encrypted Minimal Router backup (.mrbak)", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Toggle appearance", exact: true }).click();
    await openSection(page, isMobile, "#security"); await expect(page.locator(".security-recovery-card")).toHaveCount(0);
    await openSection(page, isMobile, "#recovery");
  }
});

function firmwareStatus(id: string, state: UpdateOperationState, running: string, target: string): FirmwareStatus {
  return { enabled: true, running_version: running, current_version: running, operation: { id, state, from_version: "0.1.4", target_version: target } };
}

async function pauseBrowserClock(page: Page) {
  await page.clock.install({ time: new Date("2026-01-01T00:00:00Z") });
  await page.clock.pauseAt(new Date("2026-01-01T01:00:00Z"));
}

async function openUpdates(page: Page) {
  await page.getByTitle("Account", { exact: true }).click();
  await page.getByRole("button", { name: "Software update", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Software update" })).toBeVisible();
}

async function openSection(page: Page, isMobile: boolean | undefined, hash: string) {
  if (isMobile) await page.getByRole("button", { name: "Open navigation" }).click();
  await page.locator(`a[href="${hash}"]`).click();
}

async function advanceFirmwarePoll(page: Page, r: Awaited<ReturnType<typeof router>>, status: FirmwareStatus, interval: number) {
  const prior = r.firmware.requests; r.firmware.status = status;
  await page.clock.runFor(interval);
  await expect.poll(() => r.firmware.requests).toBeGreaterThan(prior);
  await expect(page.locator(".update-dialog-versions div").filter({ hasText: "Running now" }).locator("dd")).toHaveText(status.running_version!);
}

test("firmware initial historical success never reloads or clears a new upload draft", async ({ page }) => {
  await pauseBrowserClock(page);
  const r = await router(page, { firmwareStatus: firmwareStatus("old", "succeeded", "v0.1.4", "0.1.4") });
  await page.goto("/#network"); await openUpdates(page);
  await page.getByText("Install a signed build from a file", { exact: true }).click();
  await page.locator('[name="manifest"]').setInputFiles({ name: "new.manifest.json", mimeType: "application/json", buffer: Buffer.from("{}") });
  await page.locator('[name="archive"]').setInputFiles({ name: "new.tar.gz", mimeType: "application/gzip", buffer: Buffer.from("test-only") });
  await page.clock.runFor(2000); expect(r.documents()).toBe(1);
  const requests = r.firmware.requests; await page.clock.runFor(60000);
  await expect.poll(() => r.firmware.requests).toBeGreaterThan(requests);
  await page.clock.runFor(2000); expect(r.documents()).toBe(1);
  expect(await page.locator('[name="manifest"]').evaluate((e: HTMLInputElement) => e.files?.[0]?.name)).toBe("new.manifest.json");
  expect(await page.locator('[name="archive"]').evaluate((e: HTMLInputElement) => e.files?.[0]?.name)).toBe("new.tar.gz");
  expect(await page.evaluate(() => sessionStorage.getItem("minimalrouter:completed-update-reload"))).toBeNull();
});

test("firmware observed completion reloads once only on its running target and supports sequential updates", async ({ page }) => {
  await pauseBrowserClock(page);
  const r = await router(page, { firmwareStatus: firmwareStatus("first", "checking_health", "0.1.4", "0.1.5") });
  await page.goto("/#network"); await openUpdates(page);
  await expect(page.getByText("Checking the new version", { exact: true })).toBeVisible();
  await advanceFirmwarePoll(page, r, firmwareStatus("first", "succeeded", "0.1.4", "0.1.5"), 3000);
  await expect(page.getByText("Update complete", { exact: true })).toBeVisible();
  await page.clock.runFor(2000); expect(r.documents()).toBe(1);
  await advanceFirmwarePoll(page, r, firmwareStatus("first", "succeeded", "v0.1.5", "0.1.5"), 60000);
  await page.clock.runFor(1000); await expect.poll(r.documents).toBe(2);
  await expect(page.locator(".dashboard-app")).toBeVisible(); await page.clock.runFor(2000); expect(r.documents()).toBe(2);
  await openUpdates(page);
  await advanceFirmwarePoll(page, r, firmwareStatus("second", "queued", "v0.1.5", "0.1.6"), 60000);
  await expect(page.getByText("Preparing", { exact: true })).toBeVisible();
  await advanceFirmwarePoll(page, r, firmwareStatus("second", "succeeded", "v0.1.6", "0.1.6"), 3000);
  await page.clock.runFor(1000); await expect.poll(r.documents).toBe(3);
  await expect(page.locator(".dashboard-app")).toBeVisible(); await page.clock.runFor(2000); expect(r.documents()).toBe(3);
});

test("firmware another-tab completion between idle polls reloads on the running-version transition", async ({ page }) => {
  await pauseBrowserClock(page);
  const r = await router(page, { firmwareStatus: firmwareStatus("old", "succeeded", "0.1.4", "0.1.4") });
  await page.goto("/#network"); await openUpdates(page);
  await advanceFirmwarePoll(page, r, firmwareStatus("other-tab", "succeeded", "v0.1.5", "0.1.5"), 60000);
  await page.clock.runFor(1000); await expect.poll(r.documents).toBe(2);
  await expect(page.locator(".dashboard-app")).toBeVisible(); await page.clock.runFor(2000); expect(r.documents()).toBe(2);
});

test("firmware unmount cancels a scheduled reload without consuming its persistent claim", async ({ page }) => {
  await pauseBrowserClock(page);
  const r = await router(page, { firmwareStatus: firmwareStatus("new", "checking_health", "0.1.4", "0.1.5") });
  await page.goto("/#network"); await openUpdates(page);
  await advanceFirmwarePoll(page, r, firmwareStatus("new", "succeeded", "0.1.5", "0.1.5"), 3000);
  await expect(page.getByText("Update complete", { exact: true })).toBeVisible();
  await page.getByRole("dialog").locator(".update-dialog-close").click();
  await page.getByTitle("Account", { exact: true }).click();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeVisible();
  await page.clock.runFor(2000); expect(r.documents()).toBe(1);
  expect(await page.evaluate(() => sessionStorage.getItem("minimalrouter:completed-update-reload"))).toBeNull();
});

for (const outcome of ["failed", "rolling_back", "rolled_back", "recovery_required"] as const) {
  test(`firmware ${outcome} never reloads even if target briefly became running`, async ({ page }) => {
    await pauseBrowserClock(page);
    const r = await router(page, { firmwareStatus: firmwareStatus("new", "activating", "0.1.4", "0.1.5") });
    await page.goto("/#network"); await openUpdates(page);
    await advanceFirmwarePoll(page, r, firmwareStatus("new", outcome, "0.1.5", "0.1.5"), 3000);
    await page.clock.runFor(2000); expect(r.documents()).toBe(1);
  });
}

test("cancelling reservation preview retains the input and sends no PUT", async ({ page }) => {
  const r = await router(page); page.on("dialog", d => d.dismiss()); await page.goto("/#network");
  const editor = page.locator(".static-leases");
  await editor.getByLabel("Device name", { exact: true }).fill("test-draft");
  await editor.getByLabel("MAC address", { exact: true }).fill("02:00:00:00:01:50");
  await editor.getByLabel("Reserved address", { exact: true }).fill("192.168.1.20");
  await editor.getByRole("button", { name: "Reserve address", exact: true }).click();
  await expect.poll(() => r.writes.filter(w => w.path.endsWith("/preview")).length).toBe(1);
  await expect(editor.getByLabel("Device name", { exact: true })).toHaveValue("test-draft");
  expect(r.writes.filter(w => w.path === "/api/v1/config")).toHaveLength(0);
});

for (const [label, qos] of [
  ["QoS unavailable", undefined],
  ["QoS unavailable", { available: false, devices: [] }],
  ["QoS Not applied", { available: true, devices: [] }],
  ["QoS Active", { available: true, devices: [
    { interface: "ppp0", kind: "cake", root: true },
    { interface: "ppp0", kind: "ingress", root: false },
    { interface: "ifb0", kind: "cake", root: true },
  ] }],
] as const) {
  test(`QoS card uses runtime evidence: ${label} (${qos?.available ?? "absent"})`, async ({ page }) => {
    const r = await router(page, { qos: qos && { available: qos.available, devices: [...qos.devices] } });
    r.config.qos.enabled = true; r.config.qos.algorithm = "cake"; r.config.wan.enabled = true;
    await page.goto("/#qos");
    await expect(page.locator("#qos .classic-status-chip")).toHaveText(label);
    if (label !== "QoS Active") await expect(page.locator("#qos")).not.toContainText("qdisc detected");
  });
}

for (const status of [200, 409, 422]) {
  test(`WG first-use setup uses authoritative false despite redaction (preview ${status})`, async ({ page }) => {
    const r = await router(page, { wgPreview: { status, server_key_configured: false } });
    r.config.wireguard.enabled = false;
    await page.goto("/#wireguard");
    await expect(page.getByRole("button", { name: "Enable interface", exact: true })).toHaveCount(0);
    const setup = page.getByRole("button", { name: "Set up WireGuard", exact: true });
    await expect(setup).toHaveAccessibleDescription("Add your first remote device below to set up and enable WireGuard.");
    await setup.focus(); await setup.press("Enter");
    await expect(page.getByLabel("Device name", { exact: true })).toBeFocused();
    await expect(page.getByRole("button", { name: "Generate and Download", exact: true })).toBeDisabled();
    await expect(page.getByText(/Save a Dynamic DNS hostname first/)).toBeVisible();
    expect(r.writes).toEqual([]);
  });
}

test("WG first-use setup provisions through the existing backend flow and downloads the one-time configuration", async ({ page }) => {
  const r = await router(page, { wgPreview: { server_key_configured: false } });
  r.config.wireguard.enabled = false; r.config.cloudflare.domain = "router.example.test";
  await page.route("**/api/v1/wireguard/peers", async route => {
    expect(route.request().method()).toBe("POST");
    expect(route.request().postDataJSON()).toEqual({ name: "First device" });
    const peer = { id: "test-first-peer", name: "First device", public_key: "A".repeat(43) + "=", enabled: true, allowed_ips: ["10.8.0.2/32"] };
    Object.assign(r.config.wireguard, { enabled: true, private_key: "[REDACTED]", peers: [peer] });
    r.config.revision++; r.wgPreview.server_key_configured = true;
    await route.fulfill({ json: { peer, client_config: "TEST-ONLY-CONFIGURATION", qr_code_data: "", tx: { state: "Committed" } } });
  });
  await page.goto("/#wireguard");
  await page.getByRole("button", { name: "Set up WireGuard", exact: true }).click();
  await page.getByLabel("Device name", { exact: true }).fill("First device");
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Generate and Download", exact: true }).click();
  expect((await download).suggestedFilename()).toMatch(/\.conf$/);
  await expect(page.getByText("Success! Configuration generated for First device.")).toBeVisible();
  await page.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page.getByRole("button", { name: "Disable interface", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Set up WireGuard", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Delete peer First device", exact: true })).toBeVisible();
  expect(r.writes.filter(w => w.path === "/api/v1/config" || w.path.endsWith("/config/preview"))).toEqual([]);
});

test("WG configured server enables with a redacted key and retains preview cancellation", async ({ page }) => {
  const r = await router(page);
  Object.assign(r.config.wireguard, { enabled: false, private_key: "[REDACTED]" });
  await page.goto("/#wireguard");
  await expect(page.getByRole("button", { name: "Set up WireGuard", exact: true })).toHaveCount(0);
  page.once("dialog", d => d.dismiss());
  await page.getByRole("button", { name: "Enable interface", exact: true }).click();
  await expect.poll(() => r.writes.filter(w => w.path.endsWith("/config/preview")).length).toBe(1);
  await expect(page.getByRole("button", { name: "Enable interface", exact: true })).toBeEnabled();
  expect(r.writes.filter(w => w.path === "/api/v1/config")).toEqual([]);
  expect(r.config.wireguard.enabled).toBe(false);
  page.once("dialog", d => d.accept());
  await page.getByRole("button", { name: "Enable interface", exact: true }).click();
  await expect(page.getByRole("button", { name: "Disable interface", exact: true })).toBeVisible();
  const applies = r.writes.filter(w => w.path === "/api/v1/config");
  expect(applies).toHaveLength(1);
  expect(applies[0].body.wireguard).toMatchObject({ enabled: true, private_key: "[REDACTED]" });
  await expect(page.getByText("[REDACTED]", { exact: true })).toHaveCount(0);
});

for (const status of [200, 503]) {
  test(`WG unknown setup status retains validated compatibility flow (preview ${status})`, async ({ page }) => {
    const r = await router(page, { wgPreview: { status, server_key_configured: undefined } });
    r.config.wireguard.enabled = false;
    await page.route("**/api/v1/config/preview", async route => {
      r.writes.push({ path: "/api/v1/config/preview", body: route.request().postDataJSON() });
      await route.fulfill({ status: 422, json: { error: "Server setup required" } });
    });
    await page.goto("/#wireguard");
    await expect.poll(r.previews).toBe(1);
    await expect(page.getByRole("button", { name: "Enable interface", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Set up WireGuard", exact: true })).toHaveCount(0);
    const check = page.getByRole("button", { name: "Check and enable", exact: true });
    await expect(check).toHaveAccessibleDescription(/Server setup status is unavailable/);
    await check.click();
    await expect(page.getByRole("alert")).toContainText("Server setup required");
    expect(r.writes.filter(w => w.path === "/api/v1/config")).toEqual([]);
    expect(r.config.wireguard.enabled).toBe(false);
  });
}

test("WG enabled interface can still be disabled when the preview flag is unavailable", async ({ page }) => {
  const r = await router(page, { wgPreview: { status: 503, server_key_configured: undefined } });
  page.on("dialog", d => d.accept()); await page.goto("/#wireguard");
  await page.getByRole("button", { name: "Disable interface", exact: true }).click();
  await expect.poll(() => r.config.wireguard.enabled).toBe(false);
  await expect(page.getByRole("button", { name: "Check and enable", exact: true })).toBeVisible();
});

test("WG stale configured flag becomes unknown when preview refresh fails", async ({ page }) => {
  await page.clock.install(); const r = await router(page);
  r.config.wireguard.enabled = false; await page.goto("/#wireguard");
  await expect(page.getByRole("button", { name: "Enable interface", exact: true })).toBeVisible();
  r.wgPreview.status = 503; r.wgPreview.server_key_configured = undefined;
  await page.clock.fastForward(16_000);
  await expect(page.getByRole("button", { name: "Check and enable", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Enable interface", exact: true })).toHaveCount(0);
  expect(r.writes).toEqual([]);
});
