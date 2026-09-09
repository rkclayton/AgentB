import { expect, test } from "@playwright/test";

test("uses the installed Microsoft Edge channel", async ({ page }) => {
  await page.setContent("<main>Agent_b Playwright verification</main>");
  await expect(page.locator("main")).toBeVisible();
  await expect.poll(() => page.evaluate(() => navigator.userAgent)).toMatch(/Edg\//);
});
