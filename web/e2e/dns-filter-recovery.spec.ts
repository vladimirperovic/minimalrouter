import { expect, test, type Page } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";
import type { DNSFilterPolicy } from "../src/api-types";

const profile = { id: "kids", name: "Kids tablet", enabled: true, ip_addresses: ["192.168.1.50"], services: ["youtube"], schedule: { day_windows: { monday: [{ start: "19:30", end: "22:30" }] } } };
async function fixture(page: Page) {
  let config = { ...structuredClone(CONFIG), adguard: { ...CONFIG.adguard, device_profiles: [structuredClone(profile)] } };
  let policy: DNSFilterPolicy = { revision: 7, categories: {}, exceptions: [] };
  const writes: { path: string; body: unknown }[] = [];
  await page.addInitScript(() => localStorage.setItem("minimalrouter:wan-speed-estimate-attempt", String(Date.now())));
  await page.route("**/api/v1/**", route => {
    const req = route.request(), path = new URL(req.url()).pathname, method = req.method();
    const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    const body = req.headers()["content-type"]?.includes("json") || path === "/api/v1/config" || path === "/api/v1/config/preview" ? req.postData() ? req.postDataJSON() : null : req.postData();
    if (method !== "GET") writes.push({ path, body });
    if (path === "/api/v1/config/preview") return json(body.revision === config.revision ? { changes: ["Update device schedule"], risk: "low" } : { error: "Configuration changed in another session. Reload before saving." }, body.revision === config.revision ? 200 : 409);
    if (path === "/api/v1/config") { if (method === "PUT") { config = body; return json({ state: "Committed" }); } return json(config); }
    if (path === "/api/v1/dns-filter") {
      if (method === "PUT") { policy = { ...body, revision: policy.revision + 1 }; return json({ updating: true }, 202); }
      return json({ policy, domains: Object.values(policy.categories).filter(Boolean).length * 1000, healthy: true, applied_at: 1791417600, updating: false, lists: ["threats", "ads", "adult", "gambling"].map(category => ({ category, label: category, url: "https://example.org/list", entries: 1000, updated_at: 1791417600 })), next_refresh_at: 1791504000, router_time: "2026-10-08T12:00:00+02:00", timezone: "Europe/Podgorica" });
    }
    if (path === "/api/v1/dns-filter/check") return json({ domain: body.domain, action: "Block", exception: false, healthy: true, matches: [{ category: "adult", domain: "example.com", enabled: true }] });
    if (path === "/api/v1/backup/export") return route.fulfill({ contentType: "application/vnd.minimalrouter.backup+json", headers: { "Content-Disposition": "attachment; filename=test.mrbak" }, body: "encrypted test fixture" });
    if (path === "/api/v1/backup/import/preview") return json({ import_id: "preview", expires_in_seconds: 600, candidate: config, dns_filter: { revision: 2, categories: { adult: true }, exceptions: [{ domain: "school.example.com", reason: "School" }] } });
    if (path === "/api/v1/import/backup/preview/apply") return json({ state: "Committed" });
    const responses: Record<string, unknown> = { "/api/v1/auth/session": { authenticated: true, csrf_token: "test" }, "/api/v1/system": SYSTEM, "/api/v1/health": HEALTH, "/api/v1/gateway/summary": GW_SUMMARY, "/api/v1/gateway/settings": GW_SETTINGS, "/api/v1/snapshots": [], "/api/v1/devices/pauses": { pauses: [] } };
    return json(responses[path] ?? {});
  });
  page.on("dialog", dialog => void dialog.accept());
  return { writes, concurrentEdit: () => { config = { ...config, revision: 43, adguard: { ...config.adguard, device_profiles: [...config.adguard.device_profiles, { ...profile, id: "other", name: "Other" }] } }; } };
}

test("profile rename preserves minute precision and disabled global filter", async ({ page }) => {
  const state = await fixture(page); await page.goto("/#dns-filter");
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await page.getByLabel("Profile name", { exact: true }).fill("Renamed tablet");
  await page.getByRole("button", { name: "Save profile", exact: true }).click();
  await expect(page.getByRole("dialog")).toBeHidden();
  const put = state.writes.find(write => write.path === "/api/v1/config")!.body as { adguard: { enabled: boolean; device_profiles: typeof profile[] } };
  expect(put.adguard.enabled).toBe(false); expect(put.adguard.device_profiles[0].schedule).toEqual(profile.schedule);
  expect(put.adguard.device_profiles[0].name).toBe("Renamed tablet");
});

test("concurrent profile edit keeps its original revision and cannot drop a newer profile", async ({ page }) => {
  const state = await fixture(page); await page.goto("/#dns-filter");
  await page.getByRole("button", { name: "Edit", exact: true }).click(); state.concurrentEdit();
  await page.getByLabel("Profile name", { exact: true }).fill("Old editor");
  await page.getByRole("button", { name: "Save profile", exact: true }).click();
  await expect(page.getByText("Configuration changed in another session. Reload before saving.")).toBeVisible();
  expect(state.writes.some(write => write.path === "/api/v1/config")).toBe(false);
  expect((state.writes.find(write => write.path.endsWith("/preview"))!.body as { revision: number }).revision).toBe(42);
});

test("network category changes and exceptions require explicit apply; domain checker explains parents", async ({ page }) => {
  const state = await fixture(page); await page.goto("/#dns-filter");
  await page.getByLabel(/Adult content/).check();
  await page.getByLabel("Domain to allow").fill("school.example.com"); await page.getByLabel("Reason (optional)").fill("School");
  await page.getByRole("button", { name: "Add exception", exact: true }).click();
  expect(state.writes).toEqual([]);
  await page.getByRole("button", { name: "Apply protection", exact: true }).click();
  await expect(page.getByRole("button", { name: "Check domain", exact: true })).toBeEnabled();
  const put = state.writes.find(write => write.path === "/api/v1/dns-filter")!.body as DNSFilterPolicy;
  expect(put).toEqual({ revision: 7, categories: { adult: true }, exceptions: [{ domain: "school.example.com", reason: "School" }] });
  await page.getByLabel("Domain name", { exact: true }).fill("cdn.example.com");
  await page.getByRole("button", { name: "Check domain", exact: true }).click();
  await expect(page.locator(".dns-domain-result")).toContainText("example.com");
  await expect(page.locator(".dns-domain-result")).toContainText("not proof of a blocked query or a visit");
});

test("Recovery exports with one dashboard password and keeps legacy restore optional", async ({ page }) => {
  const state = await fixture(page); await page.goto("/#recovery");
  const exportForm = page.locator("form").filter({ has: page.getByRole("button", { name: "Export encrypted backup", exact: true }) });
  await expect(exportForm.locator('input[type="password"]')).toHaveCount(1);
  await exportForm.locator('input[name="current_password"]').fill("Abcd1234!?xy");
  const download = page.waitForEvent("download"); await page.getByRole("button", { name: "Export encrypted backup", exact: true }).click(); await download;
  expect(state.writes.find(write => write.path.endsWith("/export"))!.body).toEqual({ current_password: "Abcd1234!?xy" });
  await expect(exportForm.locator('input[type="password"]')).toHaveValue("");
  await expect(page.locator('input[name="restore_backup_passphrase"]')).toHaveCount(0);
  await page.getByLabel("This backup uses an older or separate password").check();
  await expect(page.getByLabel("Password used to create this backup")).toBeVisible();
});

test("Recovery restores DNS policy only after the separate explicit continuation", async ({ page }) => {
  const state = await fixture(page); await page.goto("/#recovery");
  await page.getByLabel("Backup file", { exact: true }).setInputFiles({ name: "test.mrbak", mimeType: "application/json", buffer: Buffer.from("encrypted fixture") });
  await page.locator('input[name="restore_current_password"]').fill("Abcd1234!?xy");
  await page.getByRole("button", { name: "Validate backup", exact: true }).click();
  await expect(page.getByText("Validated restore candidate")).toBeVisible();
  await expect(page.locator('input[name="restore_current_password"]')).toHaveValue("");
  await page.getByRole("button", { name: "Apply validated backup", exact: true }).click();
  await expect(page.getByRole("button", { name: "Restore DNS protection", exact: true })).toBeVisible();
  expect(state.writes.some(write => write.path === "/api/v1/dns-filter")).toBe(false);
  await page.getByRole("button", { name: "Restore DNS protection", exact: true }).click();
  await expect(page.getByText(/DNS policy restoration requested/)).toBeVisible();
  expect(state.writes.find(write => write.path === "/api/v1/dns-filter")!.body).toEqual({ revision: 7, categories: { adult: true }, exceptions: [{ domain: "school.example.com", reason: "School" }] });
});

for (const design of ["noema", "studio"]) for (const mode of ["light", "dark"]) {
  test(`${design} ${mode}: page cards have a consistent gap including the Overview boundary`, async ({ page, isMobile }) => {
    await fixture(page); await page.addInitScript(({ design, mode }) => { localStorage.setItem("minimalrouter:design", design); localStorage.setItem("minimalrouter:theme", mode); }, { design, mode });
    for (const section of ["overview", "gateway", "network", "firewall", "security", "dns-filter", "qos", "wireguard", "cloudflare", "wifi", "traffic", "dns-activity", "squid", "recovery", "logs"]) {
      await page.goto(`/#${section}`); await expect(page.locator(".dashboard-app")).toBeVisible();
      await expect(page.locator(`.dashboard-navigation a[href="#${section}"]`)).toHaveClass(/is-active/);
      const result = await page.locator(".dashboard-main").evaluate(main => {
        const required = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--card-gap"));
        const errors: string[] = [];
        for (const parent of [main, ...main.querySelectorAll(".dashboard-section,.dns-filter,.classic-dashboard-overview,.studio-overview,.overview-content-grid,.recovery-workflows,.dns-protection-columns")]) {
          const items = [...parent.children].filter(el => { const r = el.getBoundingClientRect(); return !el.matches(".dashboard-topbar") && r.width > 0 && r.height > 0 && getComputedStyle(el).position !== "fixed"; });
          for (let i = 1; i < items.length; i++) {
            const a = items[i - 1].getBoundingClientRect(), b = items[i].getBoundingClientRect();
            if (b.y > a.y + 2 && b.x < a.right && a.x < b.right && b.y - a.bottom < required - 1) errors.push(`${parent.className}: ${items[i-1].className} -> ${items[i].className}: ${Math.round(b.y - a.bottom)}px`);
          }
        }
        const overview = main.querySelector(".classic-dashboard-overview:not(.classic-security-page)"), devices = main.querySelector(".overview-devices");
        if (overview && devices && devices.getBoundingClientRect().y - overview.getBoundingClientRect().bottom < required - 1) errors.push("Overview devices touch the preceding cards");
        return { errors, overflow: document.documentElement.scrollWidth - innerWidth };
      });
      expect(result.errors, `${section} spacing`).toEqual([]); expect(result.overflow, `${section} overflow`).toBeLessThanOrEqual(1);
      if (["overview", "recovery", "dns-filter"].includes(section)) await page.screenshot({ path: test.info().outputPath(`${section}-${isMobile ? "mobile" : "desktop"}.png`), fullPage: true, scale: "css" });
    }
  });
}
