import { expect, test, type Page } from '@playwright/test';
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from './fixtures/router';

async function stub(page: Page, health: unknown = HEALTH) {
  const mutations: string[] = [];
  await page.addInitScript(() => localStorage.setItem('minimalrouter:wan-speed-estimate-attempt', String(Date.now())));
  await page.route('**/api/v1/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (!['GET','HEAD'].includes(route.request().method())) mutations.push(path);
    const responses: Record<string, unknown> = {
      '/api/v1/auth/session': { authenticated: true, csrf_token: 'test' },
      '/api/v1/config': CONFIG, '/api/v1/system': SYSTEM, '/api/v1/health': health,
      '/api/v1/gateway/summary': GW_SUMMARY, '/api/v1/gateway/settings': GW_SETTINGS,
      '/api/v1/gateway/history': { points: [] }, '/api/v1/snapshots': [], '/api/v1/devices/pauses': { pauses: [] },
    };
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify(responses[path] ?? {}) });
  });
  return mutations;
}
async function picker(page: Page) {
  const button = page.getByRole('button', { name: 'Choose theme', exact: true });
  if (await button.getAttribute('aria-expanded') !== 'true') await button.click();
  return page.getByRole('region', { name: 'Appearance preferences' });
}

test('design and color are independent, persist after reload, and switching preserves form drafts', async ({ page }) => {
  const mutations = await stub(page);
  await page.goto('/#network');
  await expect(page.locator('html')).toHaveAttribute('data-design', 'noema');
  const draft = page.locator('#network input[type="text"]').first();
  await draft.fill('a theme switch keeps this draft');
  let panel = await picker(page);
  await panel.getByRole('radio', { name: /Studio/ }).check();
  await panel.getByRole('radio', { name: 'Dark', exact: true }).check();
  await expect(page.locator('html')).toHaveAttribute('data-design', 'studio');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: 'Choose theme', exact: true })).toBeFocused();
  await expect(draft).toHaveValue('a theme switch keeps this draft');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-design', 'studio');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  panel = await picker(page);
  await panel.getByRole('radio', { name: /Noema/ }).check();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await panel.getByRole('radio', { name: 'Light', exact: true }).check();
  await page.keyboard.press('Escape');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  expect(mutations).toEqual([]);
});

for (const mode of ['light','dark']) for (const width of [390,1024,1512]) {
  test(`Studio ${mode} at ${width}px: checks expand in flow and theme controls fit`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.addInitScript(mode => { localStorage.setItem('minimalrouter:design','studio'); localStorage.setItem('minimalrouter:theme',mode); },mode);
    await stub(page);
    await page.goto('/#overview');
    await expect(page.getByRole('heading', { name: 'All clear.', exact: true })).toBeVisible();
    const drawer = page.locator('.studio-health-drawer');
    const before = (await drawer.boundingBox())!;
    await drawer.getByRole('button', { name: 'View checks' }).click();
    await expect(drawer.getByRole('region', { name: 'System checks', exact: true })).toBeVisible();
    const after = (await drawer.boundingBox())!;
    expect(after.height).toBeGreaterThan(before.height);
    const below = (await page.locator('.studio-bottom-grid').boundingBox())!;
    expect(below.y).toBeGreaterThanOrEqual(after.y + after.height);
    await drawer.getByRole('button', { name: 'Close', exact: true }).click();
    const panel = await picker(page);
    const bounds = (await panel.boundingBox())!;
    expect(bounds.x).toBeGreaterThanOrEqual(0);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
  });
}

test('Studio never turns unknown measured health into a healthy result', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('minimalrouter:design','studio'));
  await stub(page, { state: 'unknown', checks: [], headline: 'No measured result' });
  await page.goto('/#overview');
  await expect(page.getByRole('heading', { name: 'Checking in.', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'All clear.', exact: true })).toHaveCount(0);
  await expect(page.getByText('Collecting measured samples…')).toBeVisible();
});

test('appearance works when local storage is unavailable', async ({ page }) => {
  await stub(page);
  await page.addInitScript(() => Object.defineProperty(window, 'localStorage', { get() { throw new Error('Storage blocked'); } }));
  await page.goto('/#overview');
  const panel = await picker(page);
  await panel.getByRole('radio', { name: /Studio/ }).check();
  await panel.getByRole('radio', { name: 'Dark', exact: true }).check();
  await expect(page.locator('html')).toHaveAttribute('data-design','studio');
  await expect(page.locator('html')).toHaveAttribute('data-theme','dark');
});


test('Studio samples actual byte counters and clears stale throughput after a failed poll', async ({ page }) => {
  await stub(page);
  await page.addInitScript(() => localStorage.setItem('minimalrouter:design', 'studio'));
  let failed = false;
  let counter = 0;
  await page.route('**/api/v1/system', route => failed
    ? route.fulfill({ status: 503, body: '{}' })
    : route.fulfill({ contentType: 'application/json', body: JSON.stringify({ ...SYSTEM, runtime: { ...SYSTEM.runtime, rx_bytes: ++counter * 1e7, tx_bytes: counter * 1e6 } }) }));
  await page.goto('/#overview');
  await expect(page.locator('.studio-chart-line').first()).toBeVisible({ timeout: 20000 });
  failed = true;
  await expect(page.getByText('Live data unavailable', { exact: true })).toBeVisible({ timeout: 10000 });
  await expect(page.locator('.studio-chart-line')).toHaveCount(0);
  await expect(page.locator('.studio-chart-metrics strong').first()).toContainText('—');
});

test('Studio retains critical storage warnings', async ({ page }) => {
  await stub(page);
  await page.addInitScript(() => localStorage.setItem('minimalrouter:design', 'studio'));
  await page.route('**/api/v1/system', route => route.fulfill({ contentType: 'application/json', body: JSON.stringify({ ...SYSTEM, runtime: { ...SYSTEM.runtime, storage: { ...SYSTEM.runtime.storage, level: 'critical', usage_percent: 94 } } }) }));
  await page.goto('/#overview');
  await expect(page.getByRole('alert').filter({ hasText: 'Storage critical (94% used)' })).toBeVisible();
});
