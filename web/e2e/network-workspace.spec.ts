import { expect, test, type Page } from "@playwright/test";
import type { RouterConfig } from "../src/api-types";
import {
  CONFIG,
  SYSTEM,
  HEALTH,
  GW_SUMMARY,
  GW_SETTINGS,
} from "./fixtures/router";
import { networkFixture } from "./fixtures/network";
import { dismissConfirms } from "./fixtures/confirm";

async function setup(page: Page) {
  const config = structuredClone(CONFIG) as RouterConfig;
  config.dns = { records: [{ name: "nas.lan", ip: "192.168.1.20" }] };
  const lease = {
    hostname: "test-tablet",
    mac: "02:00:00:00:00:50",
    ip_address: "192.168.1.150",
    expires_at: Math.floor(Date.now() / 1000) + 3600,
  };
  const state = {
    config,
    leases: [lease],
    writes: [] as RouterConfig[],
    requests: [] as string[],
    failed: "",
    unavailableLeases: false,
    pauses: [] as Array<{ ip: string; until_unix: number }>,
    heldPause: null as null | Promise<void>,
    pauseReads: 0,
  };
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname;
    state.requests.push(`${request.method()} ${path}`);
    let body: unknown;
    const status = path === state.failed ? 503 : 200;
    if (status !== 200) body = { error: "Test service unavailable" };
    else if (path === "/api/v1/auth/session")
      body = { authenticated: true, csrf_token: "test" };
    else if (path === "/api/v1/config") {
      if (request.method() === "PUT") {
        const candidate = request.postDataJSON() as RouterConfig;
        state.writes.push(candidate);
        Object.assign(config, candidate, { revision: candidate.revision + 1 });
        body = { state: "Committed", id: "test", config };
      } else body = config;
    } else if (path === "/api/v1/config/preview")
      body = {
        changes: ["DHCP / DNS"],
        network_changes: ["Lease duration: 12h → 24h"],
        risk: "medium",
        requires_confirmation: false,
      };
    else if (path === "/api/v1/system")
      body = {
        ...SYSTEM,
        runtime: { ...SYSTEM.runtime, dhcp_leases: state.leases },
      };
    else if (path === "/api/v1/health") body = HEALTH;
    else if (path === "/api/v1/gateway/summary") body = GW_SUMMARY;
    else if (path === "/api/v1/gateway/settings") body = GW_SETTINGS;
    else if (path === "/api/v1/devices/pauses") {
      state.pauseReads++;
      body = { pauses: [...state.pauses] };
      if (state.heldPause) {
        const held = state.heldPause;
        state.heldPause = null;
        await held;
      }
    } else if (path === "/api/v1/devices/pause") {
      const data = request.postDataJSON();
      state.pauses = [{ ip: data.ip, until_unix: 0 }];
      body = { pauses: state.pauses };
    } else if (path === "/api/v1/network/devices" && state.unavailableLeases) {
      body = {
        ...networkFixture(path, config, [])!,
        lease_status: {
          available: false,
          collected_at: new Date().toISOString(),
          error: "Lease file unavailable",
          truncated: false,
          rejected_entries: 0,
        },
      };
    } else body = networkFixture(path, config, state.leases) ?? {};
    await route
      .fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      })
      .catch(() => {});
  });
  return state;
}

test("LAN draft detects a concurrent edit and never overwrites it with a fresh revision", async ({
  page,
}) => {
  const state = await setup(page);
  await page.goto("/#network");
  await page.locator('[name="dns_record_ip_0"]').fill("192.168.1.21");
  state.config.dns!.records[0].ip = "192.168.1.22";
  state.config.revision++;
  await page.getByRole("button", { name: "Save records", exact: true }).click();
  await expect(
    page
      .getByText(
        "These settings changed in another session. Your draft is kept.",
        { exact: false },
      )
      .first(),
  ).toBeVisible();
  await expect(page.locator('[name="dns_record_ip_0"]')).toHaveValue(
    "192.168.1.21",
  );
  expect(state.writes).toHaveLength(0);
  await page.getByRole("button", { name: "Load current settings" }).click();
  await expect(page.locator('[name="dns_record_ip_0"]')).toHaveValue(
    "192.168.1.22",
  );
});

test("network polling stops while hidden and retains explicitly stale lease observations", async ({
  page,
}) => {
  await page.clock.install();
  const state = await setup(page);
  await page.goto("/#network");
  await expect(page.locator(".net-device-row")).toContainText("test-tablet");
  const before = state.requests.filter((item) =>
    item.includes("network/devices"),
  ).length;
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", {
      configurable: true,
      value: true,
    });
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "hidden",
    });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.clock.fastForward(90000);
  expect(
    state.requests.filter((item) => item.includes("network/devices")),
  ).toHaveLength(before);
  state.unavailableLeases = true;
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", {
      configurable: true,
      value: false,
    });
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "visible",
    });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect(page.locator(".net-device-row")).toContainText(
    "Previously observed lease",
  );
  await expect(page.locator(".net-device-row")).toContainText("test-tablet");
  expect(state.requests.some((item) => item.includes("/accounting"))).toBe(
    false,
  );
  await page.evaluate(() => {
    location.hash = "firewall";
  });
  await expect(page.locator("#network")).toHaveCount(0);
  const after = state.requests.filter((item) =>
    item.includes("network/devices"),
  ).length;
  await page.clock.fastForward(90000);
  expect(
    state.requests.filter((item) => item.includes("network/devices")),
  ).toHaveLength(after);
});

test("late pause reads cannot undo a newer operation and Wake failures are visible", async ({
  page,
}) => {
  await page.clock.install();
  const state = await setup(page);
  await page.goto("/#network");
  await expect(
    page.getByRole("button", { name: "Pause Internet", exact: true }),
  ).toBeEnabled();
  let release!: () => void;
  state.heldPause = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.clock.fastForward(31000);
  await expect.poll(() => state.pauseReads).toBe(2);
  await page
    .getByRole("button", { name: "Pause Internet", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Until resumed", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Resume", exact: true }),
  ).toBeVisible();
  release();
  await expect(
    page.getByRole("button", { name: "Resume", exact: true }),
  ).toBeVisible();
  state.failed = "/api/v1/network/wol";
  await page.getByRole("button", { name: "Wake", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Test service unavailable",
  );
});

test("one reservation editor preserves cancellation, traps focus and uses the whole subnet", async ({
  page,
}) => {
  const state = await setup(page);
  state.config.lan.ip_address = "192.168.1.254";
  state.config.lan.cidr = "192.168.1.254/24";
  await dismissConfirms(page);
  await page.goto("/#network");
  await page
    .getByRole("button", { name: "Add reservation", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Reserved IPv4 address")).toHaveValue(
    "192.168.1.1",
  );
  await dialog.getByLabel("Device name").fill("new-device");
  await dialog.getByLabel("MAC address").fill("02:00:00:00:00:40");
  for (let i = 0; i < 10; i++) {
    await page.keyboard.press("Tab");
    await expect
      .poll(() =>
        dialog.evaluate((node) => node.contains(document.activeElement)),
      )
      .toBe(true);
  }
  await dialog.getByRole("button", { name: "Reserve IP", exact: true }).click();
  await expect
    .poll(
      () =>
        state.requests.filter((item) => item.endsWith("/config/preview"))
          .length,
    )
    .toBe(1);
  await expect(dialog.getByLabel("Device name")).toHaveValue("new-device");
  expect(state.writes).toHaveLength(0);
  await expect(
    dialog.getByRole("button", { name: "Reserve IP", exact: true }),
  ).toBeEnabled();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(
    page.getByRole("button", { name: "Add reservation", exact: true }),
  ).toBeFocused();
});

test("network layout uses shared fonts, keeps interfaces read-only and contains all content", async ({
  page,
}, testInfo) => {
  await setup(page);
  await page.goto("/#network");
  await expect(page.locator('[name="lan_interface"]')).toHaveAttribute(
    "readonly",
    "",
  );
  await expect(page.locator(".net-wan")).not.toHaveAttribute("open", "");
  expect(
    await page
      .locator("#network h2")
      .evaluate((node) => getComputedStyle(node).fontFamily.replaceAll('"', "")),
  ).toBe(
    await page
      .locator("#network")
      .evaluate((node) =>
        getComputedStyle(node).getPropertyValue("--font-display").trim().replaceAll('"', ""),
      ),
  );
  expect(
    await page
      .locator("#network")
      .evaluate((node) => node.scrollWidth <= node.clientWidth + 1),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("network-workspace.png"),
    fullPage: true,
  });
});

test("an existing pause can be resumed without a lease or traffic history", async ({ page }) => {
  const state = await setup(page);
  state.leases = [];
  state.unavailableLeases = true;
  state.pauses = [{ ip: "192.168.1.99", until_unix: 0 }];
  await page.route("**/api/v1/devices/resume", async route => {
    expect(route.request().postDataJSON().ip).toBe("192.168.1.99");
    state.pauses = [];
    await route.fulfill({ json: { pauses: [] } });
  });
  await page.goto("/#network");
  const row = page.locator(".net-device-row").filter({ hasText: "192.168.1.99" });
  await expect(row.getByRole("button", { name: "Resume", exact: true })).toBeEnabled();
  await row.getByRole("button", { name: "Resume", exact: true }).click();
  await expect(row).toHaveCount(0);
  await expect(page.getByText("Internet resumed for Paused address.")).toBeVisible();
});

test("a superseded address check cannot replace the current conflict result", async ({ page }) => {
  const state = await setup(page);
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let started = false;
  await page.route("**/api/v1/network/address-check**", async route => {
    const ip = new URL(route.request().url()).searchParams.get("ip");
    if (ip === "192.168.1.30") { started = true; await gate; }
    await route.fulfill({ json: { revision: state.config.revision, state: ip === "192.168.1.31" ? "conflict" : "no_known_conflict", conflicts: ["Address belongs to another device"], limitation: "No active scan." } }).catch(() => {});
  });
  await page.goto("/#network");
  await page.getByRole("button", { name: "Add reservation", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("MAC address").fill("02:00:00:00:00:70");
  await dialog.getByLabel("Reserved IPv4 address").fill("192.168.1.30");
  await dialog.getByRole("button", { name: "Check address", exact: true }).click();
  await expect.poll(() => started).toBe(true);
  await dialog.getByLabel("Reserved IPv4 address").fill("192.168.1.31");
  await dialog.getByRole("button", { name: "Check address", exact: true }).click();
  await expect(dialog.getByRole("status")).toContainText("Address belongs to another device");
  release();
  await expect(dialog.getByRole("status")).toContainText("Address belongs to another device");
  expect(state.writes).toHaveLength(0);
});
