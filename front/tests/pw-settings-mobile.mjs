export const baseURL = 'https://6f670b67.peerdrive.pages.dev';

export const tests = [
  {
    name: 'Settings page mobile layout',
    fn: async ({ page, ok }) => {
      await page.setViewportSize({ width: 375, height: 667 });
      await page.goto('/settings', { waitUntil: 'networkidle' });
      await page.waitForTimeout(1000);
      await page.screenshot({ path: '/tmp/pw-settings-mobile.png', fullPage: false });
      ok('screenshot taken', true);
    }
  },
  {
    name: 'LLM section on mobile',
    fn: async ({ page, ok }) => {
      await page.setViewportSize({ width: 375, height: 667 });
      await page.goto('/settings', { waitUntil: 'networkidle' });
      // Scroll to LLM section
      await page.evaluate(() => {
        document.getElementById('llm')?.scrollIntoView({ behavior: 'instant' });
      });
      await page.waitForTimeout(500);
      await page.screenshot({ path: '/tmp/pw-settings-llm-mobile.png', fullPage: false });
      ok('llm screenshot taken', true);
    }
  }
];
