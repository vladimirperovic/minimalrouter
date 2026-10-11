import { networkFixture } from "./fixtures/network";
import { expect, test, type Page } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";

async function stub(page: Page) {
  const staticName = "workstation-with-a-long-name-and-static-reservation";
  const dynamicName = "another-workstation-with-a-long-name";
  const leases = [
    { hostname: staticName, mac: "02:00:00:00:00:14", ip_address: "192.168.1.14", expires_at: Math.floor(Date.now() / 1000) + 3600 },
    { hostname: dynamicName, mac: "02:00:00:00:00:15", ip_address: "192.168.1.150", expires_at: Math.floor(Date.now() / 1000) + 3600 },
  ];
  await page.route("**/api/v1/**", async route => {
    const p = new URL(route.request().url()).pathname;
    const data: Record<string, unknown> = {
      "/api/v1/auth/session": { authenticated: true, csrf_token: "test" },
      "/api/v1/config": { ...CONFIG, dhcp: { ...CONFIG.dhcp, static_leases: [{ id: "static", hostname: staticName, mac: leases[0].mac, ip_address: leases[0].ip_address }] } },
      "/api/v1/system": { ...SYSTEM, runtime: { ...SYSTEM.runtime, dhcp_leases: leases } },
      "/api/v1/health": { ...HEALTH, state: "healthy", checks: Array.from({ length: 12 }, (_, i) => ({ id: `check-${i}`, label: `Check ${i + 1}`, state: "healthy", summary: "The service is responding and its measured state is healthy." })) },
      "/api/v1/gateway/summary": GW_SUMMARY,
      "/api/v1/gateway/settings": GW_SETTINGS,
      "/api/v1/snapshots": [],
      "/api/v1/devices/pauses": { pauses: [] },
    };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(data[p] ?? networkFixture(p, CONFIG, leases) ?? {}) });
  });
}

for (const width of [390, 1024, 1440, 1920]) {
  test(`health drawer expands in flow and closes without overlapping cards at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 });
    await stub(page);
    await page.goto("/#overview");
    const drawer = page.locator(".overview-health-drawer");
    const toggle = drawer.getByRole("button", { name: "View checks" });
    await expect(toggle).toBeVisible();
    const before = (await drawer.boundingBox())!;
    await toggle.click();
    const details = page.getByRole("region", { name: "System checks", exact: true });
    await expect(details).toBeVisible();
    const header = (await drawer.locator(".health-banner").boundingBox())!;
    const body = (await details.boundingBox())!;
    const expanded = (await drawer.boundingBox())!;
    const charts = (await page.locator(".overview-content-grid").boundingBox())!;
    expect(body.y).toBeGreaterThanOrEqual(header.y + header.height - 1);
    expect(expanded.height).toBeGreaterThanOrEqual(before.height + body.height - 1);
    expect(body.x).toBeCloseTo(header.x, 0);
    expect(body.width).toBeCloseTo(header.width, 0);
    expect(charts.y).toBeGreaterThanOrEqual(expanded.y + expanded.height);
    await drawer.getByRole("button", { name: "Close", exact: true }).click();
    await expect(details).toBeHidden();
    expect((await drawer.boundingBox())!.height).toBeCloseTo(before.height, 0);
  });

  test(`device names and badges fit and actions stay aligned at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 });
    await stub(page);
    await page.goto("/#network");
    const table = page.locator(".net-device-list");
    await expect(table.locator(".net-device-row")).toHaveCount(2);
    for (const row of await table.locator(".net-device-row").all()) {
      const boundary = (await row.boundingBox())!;
      const name = (await row.locator(".net-device-identity strong").boundingBox())!;
      expect(name.x + name.width).toBeLessThanOrEqual(boundary.x + boundary.width);
      for (const button of await row.locator("button").all()) {
        const box = (await button.boundingBox())!;
        expect(box.x).toBeGreaterThanOrEqual(boundary.x);
        expect(box.x + box.width).toBeLessThanOrEqual(boundary.x + boundary.width + 1);
      }
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
  });
}

for (const design of ['noema', 'studio']) {
  test(`${design}: Overview has one boot activity panel after navigation and reload`, async ({ page }) => {
    await page.addInitScript(design => {
      localStorage.setItem('minimalrouter:design', design);
      localStorage.setItem('minimalrouter:wan-speed-estimate-attempt', String(Date.now()));
    }, design);
    await stub(page);
    const heading = page.getByRole('heading', { name: 'Boot activity', exact: true });
    await page.goto('/#overview');
    await expect(heading).toHaveCount(1);
    await expect(page.locator('#boot-activity-title')).toHaveCount(1);
    await page.goto('/#traffic');
    await expect(heading).toHaveCount(0);
    await page.goto('/#overview');
    await expect(heading).toHaveCount(1);
    await page.reload();
    await expect(heading).toHaveCount(1);
    await page.getByRole('button', { name: 'Choose theme', exact: true }).click();
    await page.getByRole('radio', { name: design === 'noema' ? /Studio/ : /Noema/ }).check();
    await page.keyboard.press('Escape');
    await expect(heading).toHaveCount(1);
    await expect(page.locator('#boot-activity-title')).toHaveCount(1);
  });
}

for (const design of ['noema', 'studio']) for (const width of [390,1440]) {
  test(`${design} network at ${width}px: paired settings preserve a full-width device inventory`, async ({ page }) => {
    await page.setViewportSize({width,height:1000});
    await page.addInitScript(design => {
      localStorage.setItem('minimalrouter:design', design);
      localStorage.setItem('minimalrouter:wan-speed-estimate-attempt', String(Date.now()));
    },design);
    await stub(page);
    await page.goto('/#network');
    for (const grid of await page.locator('.net-config-grid').all()) {
      const a=grid.locator('form').nth(0), b=grid.locator('form').nth(1);
      await expect(a).toBeVisible();await expect(b).toBeVisible();
      const ar=(await a.boundingBox())!,br=(await b.boundingBox())!;
      if(width>1100){expect(ar.y).toBeCloseTo(br.y,0);expect(br.x).toBeGreaterThanOrEqual(ar.x+ar.width);expect(ar.height).toBeCloseTo(br.height,0);}
      else {expect(br.y).toBeGreaterThanOrEqual(ar.y+ar.height);expect(br.x).toBeCloseTo(ar.x,0);}
    }
    const section=(await page.locator('#network').boundingBox())!;
    const table=(await page.locator('.net-devices').boundingBox())!;
    expect(table.width).toBeCloseTo(section.width,0);
    for(const name of ['Save LAN','Save DHCP','Save DNS','Save records'])await expect(page.getByRole('button',{name,exact:true})).toBeVisible();
    await expect(page.locator('[name="wan_interface"]')).toBeHidden();
    await page.locator('.net-wan > summary').click();
    await expect(page.locator('[name="wan_interface"]')).toBeVisible();
    await expect(page.locator('[name="pppoe_password"]')).toHaveAttribute('type','password');
    await expect(page.locator('[name="lan_interface"]')).toHaveAttribute('readonly','');
    expect(await page.evaluate(()=>document.documentElement.scrollWidth-innerWidth)).toBeLessThanOrEqual(1);
  });
}
