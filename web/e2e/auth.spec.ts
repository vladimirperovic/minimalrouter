import { expect, test } from "@playwright/test";

test("ambiguous login failure never makes TOTP mandatory for a non-TOTP account", async ({ page }) => {
  let loginAttempts = 0;

  await page.route("**/api/v1/auth/session", async (route) => {
    await route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({ error: "Unauthorized or expired session" }),
    });
  });
  await page.route("**/api/v1/setup/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ is_configured: true }),
    });
  });
  await page.route("**/api/v1/auth/login", async (route) => {
    loginAttempts += 1;
    const body = route.request().postDataJSON() as { password?: string; totp_code?: string };
    expect(body.totp_code ?? "").toBe("");

    if (loginAttempts === 1) {
      await route.fulfill({
        status: 401,
        contentType: "application/json",
        body: JSON.stringify({ error: "TOTP code required", totp_required: "true" }),
      });
      return;
    }

    expect(body.password).toBe("correct-password");
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, csrf_token: "test-csrf", read_only: false }),
    });
  });

  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();

  const password = page.getByLabel("Password");
  await password.fill("wrong-password");
  await page.getByRole("button", { name: "Sign in" }).click();

  const totp = page.getByLabel(/Two-factor code/);
  await expect(totp).toBeVisible();
  await expect(totp).not.toHaveAttribute("required", "");
  await expect(page.getByText("Leave this blank if two-factor authentication is not configured.")).toBeVisible();

  await password.fill("correct-password");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect.poll(() => loginAttempts).toBe(2);
});
