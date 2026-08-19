import { expect, test } from "@playwright/test";

const demoOnly = process.env.MR_DEMO_E2E === "1";

test.use({ viewport: { width: 390, height: 844 } });

test("GitHub Pages demo uses the pushed mobile menu and horizontal Logs timeline", async ({ page, isMobile }) => {
  test.skip(!demoOnly, "GitHub Pages demo build only");
  test.skip(!isMobile, "mobile-only demo regression");

  await page.goto("/");
  await expect(page.locator(".dashboard-app")).toBeVisible();

  const menu = page.locator(".mobile-navigation-toggle");
  const sidebar = page.locator(".dashboard-sidebar");
  const main = page.locator(".dashboard-main");
  await expect(menu).toBeVisible();
  await expect(page.locator(".overview-service-ribbon .overview-service-chip").filter({ hasText: /^Gateway / })).toBeHidden();

  const buttonBefore = await menu.boundingBox();
  await page.evaluate(() => window.scrollTo(0, Math.min(320, Math.max(0, document.documentElement.scrollHeight - innerHeight))));
  const buttonAfter = await menu.boundingBox();
  expect(buttonBefore).not.toBeNull();
  expect(buttonAfter).not.toBeNull();
  expect(Math.abs((buttonAfter?.x ?? 0) - (buttonBefore?.x ?? 0))).toBeLessThanOrEqual(1);
  expect(Math.abs((buttonAfter?.y ?? 0) - (buttonBefore?.y ?? 0))).toBeLessThanOrEqual(1);

  await menu.click();
  await expect(sidebar).toHaveClass(/is-open/);
  await page.waitForTimeout(680);
  expect(await main.evaluate((element) => getComputedStyle(element).transform)).not.toBe("none");

  await sidebar.locator('a[href="#logs"]').click();
  await expect(sidebar).not.toHaveClass(/is-open/);
  await expect(page.locator(".classic-page-heading h1")).toHaveText("Logs");
  expect(await page.evaluate(() => window.scrollY)).toBeLessThanOrEqual(2);

  const timeline = page.locator(".startup-timeline .tl");
  await expect(timeline).toBeVisible();
  await expect(timeline.locator(".tl-item")).toHaveCount(7);
  const timelineStyle = await timeline.evaluate((element) => ({
    flow: getComputedStyle(element).gridAutoFlow,
    overflowX: getComputedStyle(element).overflowX,
  }));
  expect(timelineStyle.flow).toContain("column");
  expect(timelineStyle.overflowX).toBe("auto");

  const geometry = await page.evaluate(() => ({
    viewport: innerWidth,
    documentWidth: document.documentElement.scrollWidth,
    bodyWidth: document.body.scrollWidth,
  }));
  expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewport + 1);
  expect(geometry.bodyWidth).toBeLessThanOrEqual(geometry.viewport + 1);
});
