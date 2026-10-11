import { test, expect, type Page, type Route } from "@playwright/test";
import {
  CONFIG,
  SYSTEM,
  HEALTH,
  GW_SUMMARY,
  GW_SETTINGS,
} from "./fixtures/router";
import type { RouterConfig } from "../src/api-types";

test("Firewall history tolerates missing details and ignores unrelated events", async ({ page }) => {
  await fixture(page);
  await page.route("**/api/v1/audit/events**", route => route.fulfill({ json: { events: [
    { id: "legacy", timestamp: new Date().toISOString(), event_type: "firewall.transaction", details: null },
    { id: "login", timestamp: new Date().toISOString(), event_type: "auth.login_succeeded", details: {} },
  ] } }));
  await page.goto("/#firewall");
  await page.locator(".fw-history > summary").click();
  await expect(page.locator(".fw-history article")).toHaveCount(1);
  await expect(page.locator(".fw-history article")).toContainText("Recorded");
  await expect(page.locator(".fw-history article")).toContainText("Unknown");
});

async function fixture(page: Page) {
  const config = structuredClone(CONFIG) as RouterConfig;
  config.firewall.custom_rules = [
    {
      id: "manual",
      name: "Block external DNS",
      direction: "forward",
      action: "deny",
      protocol: "udp",
      dst_port: 53,
      enabled: true,
    },
  ];
  config.wireguard.address = "10.77.0.1/24";
  config.firewall.port_forwards = [
    {
      id: "nas",
      name: "NAS",
      protocol: "tcp",
      external_port: 8080,
      internal_ip: "192.168.1.30",
      internal_port: 80,
      enabled: true,
    },
  ];
  const state = {
    config,
    writes: [] as RouterConfig[],
    preview: [] as RouterConfig[],
    firewallReads: 0,
    systemReads: 0,
    applyInProgress: false,
    explain: null as null | ((route: Route) => Promise<void>),
  };
  await page.addInitScript(() =>
    localStorage.setItem(
      "minimalrouter:wan-speed-estimate-attempt",
      String(Date.now()),
    ),
  );
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request(),
      url = new URL(request.url()),
      path = url.pathname;
    if (path.startsWith("/api/v1/firewall/")) state.firewallReads++;
    if (path === "/api/v1/system") state.systemReads++;
    if (path === "/api/v1/firewall/explain" && state.explain)
      return state.explain(route);
    if (request.method() === "POST" && path === "/api/v1/config/preview") {
      state.preview.push(request.postDataJSON());
      return route.fulfill({
        json: {
          changes: ["Firewall"],
          risk: "medium",
          requires_confirmation: false,
          firewall_changes: ["Add deny TCP/53, scope wan"],
          firewall_warnings: [],
        },
      });
    }
    if (request.method() === "PUT" && path === "/api/v1/config") {
      state.writes.push(request.postDataJSON());
      Object.assign(config, request.postDataJSON(), {
        revision: config.revision + 1,
      });
      return route.fulfill({
        json: { id: "firewall-test", state: "Committed" },
      });
    }
    const responses: Record<string, unknown> = {
      "/api/v1/auth/session": { authenticated: true, csrf_token: "test" },
      "/api/v1/config": config,
      "/api/v1/system": SYSTEM,
      "/api/v1/health": HEALTH,
      "/api/v1/gateway/summary": GW_SUMMARY,
      "/api/v1/gateway/settings": GW_SETTINGS,
      "/api/v1/gateway/history": { points: [] },
      "/api/v1/snapshots": [],
      "/api/v1/devices/pauses": { pauses: [] },
      "/api/v1/audit/events": { events: [] },
      "/api/v1/firewall/status": {
        revision: config.revision,
        runtime: {
          available: true,
          observed_at: new Date().toISOString(),
          default_deny: true,
          loaded_revision: config.revision,
          revision_matches: true,
          configuration_match: "unverified",
          policies: { input: "drop", forward: "drop", output: "drop" },
        },
        rules: config.firewall.custom_rules.map((r) => ({
          id: r.id,
          name: r.name,
          scope: "all",
          packets: 125,
          last_increase_observed_at: null,
        })),
        warnings: [],
        engine: { apply_in_progress: state.applyInProgress },
        pending_transaction: null,
      },
      "/api/v1/firewall/activity": {
        available: false,
        collected_at: "2026-10-10T10:00:00Z",
        allowed: 1000,
        blocked: 23,
        points: [
          {
            start: "2026-10-10T09:00:00Z",
            allowed: 1000,
            blocked: 23,
            samples: 30,
          },
          { start: "2026-10-10T09:30:00Z", allowed: 0, blocked: 0, samples: 0 },
        ],
      },
    };
    return route.fulfill({ json: responses[path] ?? {} });
  });
  page.on("dialog", (dialog) => void dialog.accept());
  return state;
}

test("DNS preset adds TCP and UDP together; a renamed or altered preset cannot show Active", async ({
  page,
}) => {
  const state = await fixture(page);
  await page.goto("/#firewall");
  await page
    .getByRole("checkbox", { name: "Select Block direct external DNS" })
    .check();
  await page.getByRole("checkbox", { name: "Select Block Telnet" }).check();
  await page.getByRole("button", { name: "Review selected rules" }).click();
  await expect.poll(() => state.writes.length).toBe(1);
  expect(state.preview).toHaveLength(1);
  const rules = state.writes[0].firewall.custom_rules;
  expect(
    rules
      .filter((r) => r.preset_id === "dns")
      .map((r) => r.protocol)
      .sort(),
  ).toEqual(["tcp", "udp"]);
  expect(rules.filter((r) => r.preset_id).every((r) => r.scope === "wan")).toBe(
    true,
  );
  expect(rules.find((r) => r.id === "manual")).toEqual(
    expect.objectContaining({ dst_port: 53, protocol: "udp", enabled: true }),
  );
  const dns = page.getByRole("article", {
    name: "Block direct external DNS",
    exact: true,
  });
  await expect(dns.locator(".fw-badge")).toHaveText("Active");
  state.config.firewall.custom_rules.find(
    (r) => r.preset_id === "dns",
  )!.action = "allow";
  state.config.revision++;
  await page.reload();
  await expect(dns.locator(".fw-badge")).toHaveText("Modified");
});

test("saved history survives delayed collection; hidden Firewall stops polling without another system request", async ({
  page,
  isMobile,
}) => {
  await page.clock.install();
  const state = await fixture(page);
  await page.goto("/#firewall");
  await expect(page.locator(".fw-activity")).toContainText(
    "Saved history is still shown",
  );
  await expect(
    page.locator(".fw-chart .insights-bar-column").first(),
  ).toHaveAttribute("aria-label", /23 blocked packets/);
  expect(
    await page
      .locator(".fw-chart rect")
      .first()
      .evaluate((e) => e.getBoundingClientRect().height),
  ).toBeGreaterThan(10);
  await expect(page.locator(".fw-chart .is-gap")).toHaveCount(1);
  expect(state.systemReads).toBe(1);
  await page.getByRole("button", { name: /Allowed packets/ }).click();
  await expect(
    page.locator(".fw-chart .insights-bar-column").first(),
  ).toHaveAttribute("aria-label", /1K allowed packets/);
  if (isMobile)
    await page
      .getByRole("button", { name: "Open navigation", exact: true })
      .click();
  await page.locator('.dashboard-navigation a[href="#overview"]').click();
  await expect(page.locator(".fw-workspace")).toHaveCount(0);
  const reads = state.firewallReads;
  await page.clock.fastForward(65000);
  expect(state.firewallReads).toBe(reads);
});

test("edited flow input invalidates a late result", async ({ page }) => {
  const state = await fixture(page);
  let release = () => {};
  let first = false;
  state.explain = async (route) => {
    const source = new URL(route.request().url()).searchParams.get("source_ip");
    if (source === "192.168.1.50") {
      first = true;
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    }
    await route
      .fulfill({
        json: {
          revision: 42,
          basis: "configured_policy",
          verdict: "deny",
          reason:
            source === "192.168.1.50" ? "OLD RESULT" : "Current flow result",
          source_zone: "lan",
          destination_zone: "wan",
          checked_at: new Date().toISOString(),
          limitations: ["Not a packet probe."],
        },
      })
      .catch(() => {});
  };
  await page.goto("/#firewall");
  const form = page.locator(".fw-check-form");
  await form.getByLabel("Source IP", { exact: true }).fill("192.168.1.50");
  await form.getByLabel("Destination IP", { exact: true }).fill("1.1.1.1");
  await form.getByLabel("Destination port", { exact: true }).fill("443");
  await form.getByRole("button", { name: "Explain connection" }).click();
  await expect.poll(() => first).toBe(true);
  await form.getByLabel("Source IP", { exact: true }).fill("192.168.1.51");
  await form.getByRole("button", { name: "Explain connection" }).click();
  await expect(page.locator(".fw-flow-result")).toContainText(
    "Current flow result",
  );
  release();
  await expect(page.locator(".fw-flow-result")).not.toContainText("OLD RESULT");
  await form.getByLabel("Destination IP", { exact: true }).fill("8.8.8.8");
  await expect(page.locator(".fw-flow-result")).toHaveCount(0);
});

test("editor rejects unsafe input and stale edits; tunnel writes preserve concurrent rules", async ({
  page,
}) => {
  const state = await fixture(page);
  await page.goto("/#firewall");
  await page.getByRole("button", { name: "+ Add rule" }).click();
  let form = page.getByRole("form", { name: "New firewall rule" });
  await form
    .getByRole("combobox", { name: "Direction", exact: true })
    .selectOption("input");
  await expect(
    form
      .getByRole("combobox", { name: "Action", exact: true })
      .locator('option[value="deny"]'),
  ).toHaveJSProperty("disabled", true);
  await expect(
    form.getByRole("combobox", { name: "Action", exact: true }),
  ).toHaveValue("allow");
  await form.getByRole("button", { name: "Cancel", exact: true }).click();
  await page
    .getByRole("button", { name: "Edit Block external DNS", exact: true })
    .click();
  form = page.getByRole("form", { name: "Edit firewall rule" });
  await form.getByLabel("Rule name", { exact: true }).fill("Changed locally");
  state.config.firewall.custom_rules[0].name = "Changed elsewhere";
  state.config.revision++;
  await form.getByRole("button", { name: "Review rule" }).click();
  await expect(
    page.getByRole("alert").filter({ hasText: "changed in another session" }),
  ).toBeVisible();
  expect(state.writes).toHaveLength(0);
  await page.locator(".fw-forwards > summary").click();
  const forwards = page.locator(".classic-port-forwards");
  await expect(forwards).toContainText("10.77.0.1:8080");
  const concurrent = {
    id: "concurrent",
    name: "Another operator",
    protocol: "tcp",
    external_port: 9090,
    internal_ip: "192.168.1.40",
    internal_port: 90,
    enabled: true,
  };
  state.config.firewall.port_forwards.push(concurrent);
  state.config.revision++;
  await forwards
    .getByLabel("Rule name", { exact: true })
    .fill("New tunnel route");
  await forwards.getByLabel("Tunnel port", { exact: true }).fill("8081");
  await forwards
    .getByLabel("LAN destination", { exact: true })
    .fill("192.168.1.31");
  await forwards.getByLabel("Service port", { exact: true }).fill("81");
  await forwards
    .getByRole("button", { name: "Add tunnel rule", exact: true })
    .click();
  await expect.poll(() => state.writes.length).toBe(1);
  expect(state.writes[0].firewall.port_forwards).toContainEqual(concurrent);
});

test("apply in progress disables changes", async ({ page }) => {
  const state = await fixture(page);
  state.applyInProgress = true;
  await page.goto("/#firewall");
  await expect(page.getByRole("button", { name: "+ Add rule" })).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Pause Block external DNS", exact: true }),
  ).toBeDisabled();
});

for (const design of ["noema", "studio"])
  test(`${design}: readable responsive rules and shared heading fonts`, async ({
    page,
  }, testInfo) => {
    await page.addInitScript(
      (value) => localStorage.setItem("minimalrouter:design", value),
      design,
    );
    await fixture(page);
    await page.goto("/#firewall");
    await expect(
      page.getByRole("heading", { name: "Custom rules", exact: true }),
    ).toBeVisible();
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    expect(
      await page
        .locator(".fw-workspace")
        .evaluate((e) => e.scrollWidth <= e.clientWidth + 1),
    ).toBe(true);
    for (const selector of [".fw-rule", ".fw-preset"]) {
      const bounds = await page
        .locator(selector)
        .evaluateAll((nodes) =>
          nodes.map((e) => ({ width: e.clientWidth, scroll: e.scrollWidth })),
        );
      for (const box of bounds)
        expect(box.scroll).toBeLessThanOrEqual(box.width + 1);
    }
    const font = await page
      .locator(".fw-workspace h3")
      .first()
      .evaluate((e) => ({
        family: getComputedStyle(e).fontFamily,
        token: getComputedStyle(e).getPropertyValue("--font-display").trim(),
        weight: getComputedStyle(e).fontWeight,
      }));
    expect(font.family).toContain(design === "noema" ? "Palatino" : "Inter");
    expect(font.weight).toBe(design === "noema" ? "400" : "600");
    const facts = await page
      .locator(".fw-hero-facts dd")
      .evaluateAll((nodes) =>
        nodes.map((node) => ({
          family: getComputedStyle(node).fontFamily,
          size: parseFloat(getComputedStyle(node).fontSize),
          transform: getComputedStyle(node).textTransform,
        })),
      );
    for (const fact of facts) {
      expect(fact.family).toBe(font.family);
      expect(fact.size).toBeLessThanOrEqual(20);
      expect(fact.transform).toBe("none");
    }
    await page
      .locator(".fw-rule-list")
      .screenshot({ path: testInfo.outputPath("custom-rules.png") });
    await page
      .locator(".fw-presets-grid")
      .screenshot({ path: testInfo.outputPath("suggested-rules.png") });
    await page.screenshot({
      path: testInfo.outputPath("firewall.png"),
      fullPage: true,
    });
    expect(errors).toEqual([]);
  });
