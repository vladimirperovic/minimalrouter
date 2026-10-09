import { expect, test, type Page } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";
import type { RecoveryOperation, RecoveryPreview } from "../src/lib/recovery";

const assessment = { can_apply: true, blockers: [], changes: ["System / management"], risk: "low", requires_confirmation: false, expected_interruption: "No management interruption expected." };
function preview(): RecoveryPreview { return { source: "backup", import_id: "preview", base_revision: 42, expires_at: new Date(Date.now() + 600000).toISOString(), candidate: structuredClone(CONFIG), assessment, dns_filter: { revision: 1, categories: {}, exceptions: [] } }; }

async function fixture(page: Page) {
  const state = { config: structuredClone(CONFIG), operation: null as RecoveryOperation | null, preview: preview(), snapshotError: false, requests: [] as string[], writes: [] as { path: string; body: unknown }[] };
  await page.addInitScript(() => localStorage.setItem("minimalrouter:wan-speed-estimate-attempt", String(Date.now())));
  await page.route("**/api/v1/**", route => {
    const request = route.request(), path = new URL(request.url()).pathname, method = request.method();
    state.requests.push(`${method} ${path}`);
    if (method !== "GET") state.writes.push({ path, body: request.headers()["content-type"]?.includes("json") ? request.postDataJSON() : null });
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    if (path === "/api/v1/recovery/status") return json({ generated_at: new Date().toISOString(), revision: state.config.revision, last_backup_export_at: "2026-10-01T12:00:00Z", snapshot_count: 2, retention: { manual: 20, automatic: 20 }, pending: state.operation?.state === "awaiting_confirmation" ? { id: "restore", state: "AwaitingConfirmation", confirmation_deadline: state.operation.confirmation_deadline } : null, operation: state.operation });
    if (path === "/api/v1/snapshots") return state.snapshotError ? json({ error: "Storage temporarily unavailable" }, 503) : json({ snapshots: [{ id: "manual", label: "Before firewall changes", kind: "manual", revision: 41, created_at: "2026-10-08T12:00:00Z", checksum: "a".repeat(64) }, { id: "automatic", kind: "automatic", revision: 40, created_at: "2026-10-07T12:00:00Z", checksum: "b".repeat(64) }], retention: { manual: 20, automatic: 20 } });
    if (path.endsWith("/snapshots/manual/preview")) return json({ ...state.preview, source: "snapshot", snapshot_id: "manual", import_id: undefined, expires_at: undefined });
    if (path === "/api/v1/backup/import/preview") return json(state.preview);
    if (path === "/api/v1/import/backup/preview/apply") {
      state.operation = { id: "restore", source: "backup", transaction_id: "restore", state: "awaiting_confirmation", target_revision: 43, started_at: new Date().toISOString(), confirmation_deadline: new Date(Date.now() + 90000).toISOString(), dns_policy: state.preview.dns_filter };
      return json({ id: "restore", state: "AwaitingConfirmation", confirmation_deadline: state.operation.confirmation_deadline }, 202);
    }
    if (path.endsWith("/recovery/operations/restore/dns")) { state.operation!.state = "dns_running"; return json(state.operation, 202); }
    if (path === "/api/v1/transactions/pending") return json(state.operation?.state === "awaiting_confirmation" ? { id: "restore", state: "AwaitingConfirmation", confirmation_deadline: state.operation.confirmation_deadline } : {});
    const responses: Record<string, unknown> = { "/api/v1/auth/session": { authenticated: true, csrf_token: "test" }, "/api/v1/config": state.config, "/api/v1/system": SYSTEM, "/api/v1/health": HEALTH, "/api/v1/gateway/summary": GW_SUMMARY, "/api/v1/gateway/settings": GW_SETTINGS, "/api/v1/devices/pauses": { pauses: [] } };
    return json(responses[path] ?? {});
  });
  page.on("dialog", dialog => void dialog.accept());
  return state;
}

async function validate(page: Page) {
  await page.getByLabel("Backup file", { exact: true }).setInputFiles({ name: "test.mrbak", mimeType: "application/json", buffer: Buffer.from("test fixture") });
  await page.locator('input[name="restore_current_password"]').fill("fixture-password");
  await page.getByRole("button", { name: "Validate backup", exact: true }).click();
}

test("idle progress stays hidden, transfer cards and snapshot text align in each theme", async ({ page, isMobile }) => {
  await fixture(page);
  for (const design of ["noema", "studio"]) for (const mode of ["light", "dark"]) {
    await page.addInitScript(({ design, mode }) => { localStorage.setItem("minimalrouter:design", design); localStorage.setItem("minimalrouter:theme", mode); }, { design, mode });
    await page.goto("/#recovery");
    await expect(page.getByText("Before firewall changes", { exact: true })).toBeVisible();
    await expect(page.getByRole("region", { name: "Restore progress" })).toHaveCount(0);
    const layout = await page.locator("#recovery").evaluate(root => {
      const cards = [...root.querySelectorAll(".recovery-transfer")].map(node => node.getBoundingClientRect());
      const titles = [...root.querySelectorAll(".recovery-snapshot-info")].map(node => node.getBoundingClientRect().x);
      const buttons = [...root.querySelectorAll(".recovery-transfer .form-actions")].map(node => node.getBoundingClientRect().bottom);
      const headingFont = getComputedStyle(root.querySelector("h3")!).fontFamily;
      const displayFont = getComputedStyle(document.documentElement).getPropertyValue("--font-display").trim();
      return { heightDifference: Math.abs(cards[0].height - cards[1].height), titleDifference: Math.abs(titles[0] - titles[1]), buttonDifference: Math.abs(buttons[0] - buttons[1]), overflow: document.documentElement.scrollWidth - innerWidth, headingFont, displayFont };
    });
    if (!isMobile) { expect(layout.heightDifference).toBeLessThanOrEqual(1); expect(layout.buttonDifference).toBeLessThanOrEqual(1); }
    expect(layout.titleDifference).toBeLessThanOrEqual(1); expect(layout.overflow).toBeLessThanOrEqual(1);
    expect(layout.headingFont).not.toBe("");
    await page.screenshot({ path: test.info().outputPath(`recovery-${design}-${mode}.png`), fullPage: true, scale: "css" });
  }
});

test("snapshots poll only on visible Recovery; pending network monitoring stays global", async ({ page }) => {
  const state = await fixture(page); await page.clock.install(); await page.goto("/#recovery");
  await expect(page.getByText("Before firewall changes", { exact: true })).toBeVisible();
  const snapshots = () => state.requests.filter(item => item === "GET /api/v1/snapshots").length;
  const pending = () => state.requests.filter(item => item === "GET /api/v1/transactions/pending").length;
  await page.evaluate(() => { location.hash = "network"; }); await expect(page.locator("#recovery")).toHaveCount(0);
  const before = snapshots(), pendingBefore = pending(); await page.clock.fastForward(31000);
  await expect.poll(pending).toBeGreaterThan(pendingBefore); expect(snapshots()).toBe(before);
  await page.evaluate(() => { location.hash = "recovery"; }); await expect.poll(snapshots).toBeGreaterThan(before);
  await page.evaluate(() => { Object.defineProperty(document, "hidden", { configurable: true, value: true }); document.dispatchEvent(new Event("visibilitychange")); });
  const hiddenCount = snapshots(); await page.clock.fastForward(31000); expect(snapshots()).toBe(hiddenCount);
  await page.evaluate(() => { Object.defineProperty(document, "hidden", { configurable: true, value: false }); document.dispatchEvent(new Event("visibilitychange")); });
  await expect.poll(snapshots).toBeGreaterThan(hiddenCount);
});

test("late preview cannot replace a newer file and changing inputs discards its token", async ({ page }) => {
  await fixture(page); let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve; }); let requests = 0;
  await page.route("**/api/v1/backup/import/preview", async route => {
    const ordinal = ++requests; if (ordinal === 1) await gate;
    const body = preview(); body.candidate.system.hostname = ordinal === 1 ? "stale-host" : "latest-host";
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) }).catch(() => undefined);
  });
  await page.goto("/#recovery"); await validate(page); await expect.poll(() => requests).toBe(1);
  await validate(page); await expect(page.getByText("latest-host", { exact: true })).toBeVisible(); release();
  await expect(page.getByText("stale-host", { exact: true })).toHaveCount(0);
  await page.getByLabel("Backup file", { exact: true }).setInputFiles({ name: "different.mrbak", mimeType: "application/json", buffer: Buffer.from("other fixture") });
  await expect(page.getByRole("button", { name: "Apply validated backup", exact: true })).toHaveCount(0);
});

test("expired and stale-revision previews cannot be applied", async ({ page }) => {
  const state = await fixture(page); await page.clock.install(); await page.goto("/#recovery"); await validate(page);
  await expect(page.getByRole("button", { name: "Apply validated backup", exact: true })).toBeEnabled();
  await page.clock.fastForward(601000);
  await expect(page.getByText("This preview has expired. Validate the file again.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Apply validated backup", exact: true })).toBeDisabled();
  state.preview.expires_at = "2099-01-01T00:00:00Z"; await validate(page);
  state.config.revision = 43; await page.getByRole("button", { name: "Refresh snapshots" }).click();
  await expect(page.getByText("Configuration changed after preview. Validate again before applying.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Apply validated backup", exact: true })).toBeDisabled();
  expect(state.writes.some(write => write.path.endsWith("/apply"))).toBe(false);
});

test("pending restore survives navigation, blocks early DNS and tracks asynchronous failure and retry", async ({ page }) => {
  const state = await fixture(page); state.preview.dns_filter = { revision: 1, categories: null, exceptions: null } as unknown as RecoveryPreview["dns_filter"];
  await page.goto("/#recovery"); await validate(page); await page.getByRole("button", { name: "Apply validated backup", exact: true }).click();
  await expect(page.getByRole("button", { name: "Restore DNS protection", exact: true })).toBeDisabled();
  await page.evaluate(() => { location.hash = "network"; }); await expect(page.locator("#recovery")).toHaveCount(0);
  await page.evaluate(() => { location.hash = "recovery"; }); await expect(page.getByRole("button", { name: "Restore DNS protection", exact: true })).toBeDisabled();
  expect(state.writes.some(write => write.path.endsWith("/dns"))).toBe(false);
  state.operation!.state = "dns_pending"; state.config.revision = 43;
  await page.getByRole("button", { name: "Refresh snapshots" }).click();
  await expect(page.getByRole("button", { name: "Restore DNS protection", exact: true })).toBeEnabled();
  await page.getByRole("button", { name: "Restore DNS protection", exact: true }).click();
  await expect(page.getByRole("button", { name: "Restoring DNS…" })).toBeDisabled();
  await expect(page.getByText("Recovery completed", { exact: true })).toHaveCount(0);
  state.operation!.state = "dns_failed"; state.operation!.error = "DNS helper rejected the policy";
  await page.getByRole("button", { name: "Refresh snapshots" }).click();
  await expect(page.getByText("DNS helper rejected the policy", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Retry DNS restore", exact: true }).click();
  state.operation!.state = "completed"; state.operation!.error = "";
  await page.getByRole("button", { name: "Refresh snapshots" }).click();
  await expect(page.getByText("No unfinished restore", { exact: true })).toBeVisible();
  expect(state.writes.filter(write => write.path.endsWith("/dns"))).toHaveLength(2);
});

test("failed snapshot loading is not an empty history and retry recovers", async ({ page }) => {
  const state = await fixture(page); state.snapshotError = true; await page.goto("/#recovery");
  await expect(page.getByText("Snapshot history unavailable", { exact: true })).toBeVisible();
  await expect(page.getByText("No snapshots yet.", { exact: true })).toHaveCount(0);
  state.snapshotError = false; await page.getByRole("button", { name: "Retry snapshots" }).click();
  await expect(page.getByText("Before firewall changes", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Preview restore", exact: true }).first().click();
  await expect(page.getByRole("heading", { name: "Snapshot restore preview" })).toBeVisible();
  await expect(page.getByText("System / management", { exact: true })).toBeVisible();
});

test("diagnostic request is aborted on navigation and never downloads a late result", async ({ page }) => {
  await fixture(page); let release!: () => void, started = false, downloads = 0;
  const gate = new Promise<void>(resolve => { release = resolve; });
  page.on("download", () => { downloads++; });
  await page.route("**/api/v1/system/diagnostics", async route => { started = true; await gate; await route.fulfill({ contentType: "application/json", body: "{}" }).catch(() => undefined); });
  await page.goto("/#recovery"); await page.getByRole("button", { name: "Download diagnostics" }).click();
  await expect.poll(() => started).toBe(true);
  await page.evaluate(() => { location.hash = "network"; }); await expect(page.locator("#recovery")).toHaveCount(0); release();
  await page.evaluate(() => { location.hash = "recovery"; }); await expect(page.getByRole("button", { name: "Download diagnostics" })).toBeEnabled();
  expect(downloads).toBe(0);
});
