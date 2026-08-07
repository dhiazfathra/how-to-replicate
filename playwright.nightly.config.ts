import { defineConfig } from '@playwright/test';

const PORT = Number(process.env.HTR_NIGHTLY_PORT ?? 5179);
const BASE_URL = `http://127.0.0.1:${PORT}`;

/**
 * Separate from `playwright.config.ts` on purpose (spec §18): recording +
 * decoding + OCR-ing real video is slow, so it runs as its own nightly job
 * while the fast non-video redaction corpus keeps gating every PR. A
 * distinct `testDir` (outside `e2e/`) keeps `pnpm e2e` from ever picking
 * this suite up by accident.
 */
export default defineConfig({
  testDir: './e2e-nightly',
  outputDir: './e2e-nightly/artifacts/test-results',
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  // OCR is the slow part — well north of Playwright's 30s default.
  timeout: 120_000,
  reporter: [['list']],
  use: {
    baseURL: BASE_URL,
  },
  webServer: {
    command: 'node e2e-nightly/build.mjs && node e2e-nightly/server.mjs',
    url: `${BASE_URL}/`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
    stdout: 'pipe',
    env: { PORT: String(PORT) },
  },
});
