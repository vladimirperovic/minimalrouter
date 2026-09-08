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
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(data[p] ?? {}) });
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
    const table = page.locator(".modern-device-section .device-lan-table");
    await expect(table.locator("tbody tr")).toHaveCount(2);
    const rows = await table.locator("tbody tr").evaluateAll(nodes => nodes.map(row => {
      const rect = (selector: string) => {
        const e = row.querySelector(selector) as HTMLElement | null;
        if (!e) return null;
        const b = e.getBoundingClientRect();
        return { x: b.x, y: b.y, right: b.right, bottom: b.bottom, width: b.width, height: b.height, overflow: e.scrollWidth - e.clientWidth, title: e.getAttribute("title") };
      };
      return { cell: rect(".elegant-cell-name")!, name: rect(".device-hostname")!, badge: rect(".elegant-badge-static"),
        wake: rect(".device-action-wake")!, reserve: rect(".device-action-reserve"), pause: rect(".device-pause-button")! };
    }));
    for (const row of rows) {
      // Desktop names truncate within one line; the full value stays in title.
      if (row.name.overflow > 1) expect(row.name.title).toBeTruthy();
      expect(row.name.right).toBeLessThanOrEqual(row.cell.right);
      if (row.badge) expect(row.badge.right).toBeLessThanOrEqual(row.cell.right);
      expect(row.wake.height).toBe(row.pause.height);
      expect(row.wake.y).toBe(row.pause.y);
      if (row.reserve) {
        expect(row.reserve.height).toBe(row.pause.height);
        expect(row.reserve.y).toBe(row.pause.y);
      }
    }
    expect(rows[0].wake.x).toBe(rows[1].wake.x);
    expect(rows[0].pause.x).toBe(rows[1].pause.x);
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
  test(`${design} network at ${width}px: paired settings preserve full-width tables`, async ({ page }) => {
    await page.setViewportSize({width,height:1000});
    await page.addInitScript(design => {
      localStorage.setItem('minimalrouter:design', design);
      localStorage.setItem('minimalrouter:wan-speed-estimate-attempt', String(Date.now()));
    },design);
    await stub(page);
    await page.goto('/#network');
    for (const [left,right] of [['wan','lan'],['dhcp','dns']]) {
      const a=page.locator(`#network form[data-section="${left}"]`);
      const b=page.locator(`#network form[data-section="${right}"]`);
      await expect(a).toBeVisible();await expect(b).toBeVisible();
      const ar=(await a.boundingBox())!,br=(await b.boundingBox())!;
      if(width>1100){expect(ar.y).toBeCloseTo(br.y,0);expect(br.x).toBeGreaterThanOrEqual(ar.x+ar.width);expect(ar.height).toBeCloseTo(br.height,0);}
      else {expect(br.y).toBeGreaterThanOrEqual(ar.y+ar.height);expect(br.x).toBeCloseTo(ar.x,0);}
    }
    const section=(await page.locator('#network').boundingBox())!;
    const table=(await page.locator('.modern-device-section').boundingBox())!;
    expect(table.width).toBeCloseTo(section.width,0);
    await expect(page.locator('.modern-device-section table')).toBeVisible();
    for(const name of ['Save WAN','Save LAN','Save DHCP','Save DNS'])await expect(page.getByRole('button',{name,exact:true})).toBeVisible();
    await expect(page.locator('[name="wan_interface"]')).toBeVisible();
    await expect(page.locator('[name="pppoe_password"]')).toHaveAttribute('type','password');
    await expect(page.locator('[name="lan_prefix_display"]')).toBeDisabled();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth-innerWidth)).toBeLessThanOrEqual(1);
  });
}
