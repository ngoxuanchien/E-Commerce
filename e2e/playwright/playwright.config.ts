import { defineConfig, devices } from '@playwright/test';
import { WEB_URL } from './support/api';

// The whole suite shares one real stack and the gateway's per-IP rate limits,
// so tests run one at a time.
export default defineConfig({
  testDir: 'tests',
  fullyParallel: false,
  workers: 1,
  timeout: 120_000,
  expect: { timeout: 10_000 },
  retries: process.env.CI ? 1 : 0,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],
  use: {
    ...devices['Desktop Chrome'],
    baseURL: WEB_URL,
    locale: 'vi-VN',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
});
