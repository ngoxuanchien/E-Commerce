import { defineConfig, devices } from '@playwright/test';

const MOCK_URL = 'http://localhost:5174';
const REAL_URL = 'http://localhost:5175';
const reuse = !process.env.CI;

export default defineConfig({
  testDir: 'e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: 'list',
  use: { trace: 'retain-on-failure', locale: 'vi-VN' },
  projects: [
    { name: 'mock-desktop', testIgnore: /session-real/, use: { ...devices['Desktop Chrome'], baseURL: MOCK_URL } },
    { name: 'mock-360', testMatch: /(buyer|pages)\.spec/, use: { ...devices['Pixel 7'], viewport: { width: 360, height: 780 }, baseURL: MOCK_URL } },
    { name: 'real-stub', testMatch: /session-real/, use: { ...devices['Desktop Chrome'], baseURL: REAL_URL } },
  ],
  webServer: [
    { command: 'npm run dev:mock -- --port 5174 --strictPort', url: MOCK_URL, reuseExistingServer: reuse },
    { command: 'node e2e/stub-gateway.mjs', url: 'http://127.0.0.1:18080/healthz', reuseExistingServer: reuse },
    {
      command: 'VITE_CACHE_DIR=node_modules/.vite-real VITE_API_PROXY_TARGET=http://127.0.0.1:18080 npx vite --port 5175 --strictPort',
      url: REAL_URL,
      reuseExistingServer: reuse,
    },
  ],
});
