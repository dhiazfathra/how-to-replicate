/// <reference types="vite/client" />
import { describe, expect, it } from 'vitest';

/**
 * Offscreen documents cannot use `chrome.tabs.*` in MV3 — only
 * `chrome.runtime` plus a small allowlist. `main.ts` learned this the hard
 * way (it called `chrome.tabs.captureVisibleTab()` directly, which throws at
 * runtime in a real browser even though unit tests — which inject a fake
 * `ScreenshotCapture` — never exercise the real API). `main.ts` is excluded
 * from coverage (it's pure wiring against ambient globals a test can't
 * provide), so a source-text scan is the only thing that would have caught
 * this regression.
 */
const sources = import.meta.glob<string>('./*.ts', { query: '?raw', import: 'default', eager: true });

describe('offscreen document source', () => {
  it('never references chrome.tabs — only the service worker may call it', () => {
    const files = Object.entries(sources).filter(([path]) => !path.endsWith('.test.ts'));
    expect(files.length).toBeGreaterThan(0);

    for (const [path, source] of files) {
      expect(source, `${path} must not reference chrome.tabs`).not.toMatch(/chrome\.tabs/);
    }
  });
});
