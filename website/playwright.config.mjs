import { defineConfig } from '@playwright/test';

const url = 'http://127.0.0.1:41737';

export default defineConfig({
  testDir: './tests',
  outputDir: '../test-results/website/playwright',
  workers: process.env.CI ? 2 : 1,
  timeout: 45_000,
  forbidOnly: !!process.env.CI,
  use: {
    baseURL: url,
    actionTimeout: 8_000,
    navigationTimeout: 12_000,
    launchOptions: { executablePath: process.env.WEBCRATE_CHROMIUM },
    trace: 'retain-on-failure',
  },
  webServer: { command: 'node preview.mjs', url, timeout: 10_000 },
  projects: [
    { name: 'desktop', use: { viewport: { width: 1440, height: 1000 } } },
    { name: 'mobile', use: { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } },
  ],
});
