import { defineConfig } from '@playwright/test';
import { BASE_URL, PORT } from './e2e/support.js';

const VIEWPORT = { width: 1100, height: 720 };
/** Small on purpose: the recorded videos are committed as evidence. */
const VIDEO_SIZE = { width: 800, height: 524 };

export default defineConfig({
  testDir: './e2e',
  outputDir: './e2e/artifacts/test-results',
  // Every spec drives the same origin's IndexedDB through a real browser;
  // serialising them keeps the recorded evidence readable and the DB
  // assertions unambiguous.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 90_000,
  reporter: [
    ['list'],
    ['html', { outputFolder: './e2e/artifacts/report', open: 'never' }],
  ],
  globalTeardown: './e2e/collect-evidence.ts',
  use: {
    baseURL: BASE_URL,
    viewport: VIEWPORT,
    // Always on, pass or fail: the video *is* the deliverable.
    video: { mode: 'on', size: VIDEO_SIZE },
    trace: 'on-first-retry',
  },
  webServer: {
    command: 'node e2e/build.mjs && node e2e/server.mjs',
    url: `${BASE_URL}/`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
    stdout: 'pipe',
    env: { PORT: String(PORT) },
  },
});
