import { chromium, expect, test, type BrowserContext, type Worker } from '@playwright/test';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import type { CaptureEvent } from '@htr/capture-core';
import { BASE_URL } from './support.js';

const here = path.dirname(fileURLToPath(import.meta.url));
const EXTENSION_DIR = path.resolve(here, '../clients/extension/dist');
const VIDEO_DIR = path.join(here, 'artifacts/test-results/extension');

/**
 * Minimal typing for the `chrome` global inside the extension's service
 * worker. Declared (never defined) so the arrow functions handed to
 * `worker.evaluate` type-check without pulling in `@types/chrome`.
 */
declare const chrome: {
  runtime: {
    getManifest(): {
      name: string;
      manifest_version: number;
      action?: { default_popup?: string };
      commands?: Record<string, unknown>;
    };
    onMessage: { addListener(listener: (message: unknown) => void): void };
  };
  action: { onClicked: { hasListeners(): boolean } };
  storage: { managed: { get(keys: string | null): Promise<Record<string, unknown>> } };
  tabs: {
    query(info: { url?: string }): Promise<{ id?: number; url?: string }[]>;
    sendMessage(tabId: number, message: unknown): Promise<unknown>;
  };
};
/** Collector installed on the service worker's `globalThis` by the test. */
declare const __htrSeen: unknown[];

async function serviceWorkerOf(context: BrowserContext): Promise<Worker> {
  return context.serviceWorkers()[0] ?? (await context.waitForEvent('serviceworker'));
}

test('the MV3 extension loads in real Chrome, injects its content script, and stays fail-closed', async () => {
  const userDataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'htr-e2e-profile-'));
  fs.mkdirSync(VIDEO_DIR, { recursive: true });

  expect(
    fs.existsSync(path.join(EXTENSION_DIR, 'manifest.json')),
    'clients/extension/dist is not built — run `pnpm build` first',
  ).toBe(true);

  const context = await chromium.launchPersistentContext(userDataDir, {
    channel: 'chromium',
    args: [
      `--disable-extensions-except=${EXTENSION_DIR}`,
      `--load-extension=${EXTENSION_DIR}`,
    ],
    viewport: { width: 1100, height: 720 },
    recordVideo: { dir: VIDEO_DIR, size: { width: 800, height: 524 } },
  });

  try {
    // --- the service worker registers and boots ---------------------------
    const worker = await serviceWorkerOf(context);
    expect(worker.url()).toContain('background/main.js');

    const boot = await worker.evaluate(() => ({
      manifest: chrome.runtime.getManifest(),
      // `main.ts`'s last statement. If it is registered, `wireServiceWorker`
      // and `createCaptureController` both ran to completion without throwing.
      actionListenerRegistered: chrome.action.onClicked.hasListeners(),
    }));
    expect(boot.manifest.name).toBe('How to Replicate');
    expect(boot.manifest.manifest_version).toBe(3);
    expect(boot.actionListenerRegistered, 'the background entry point did not finish booting').toBe(
      true,
    );

    // --- the content script injects into a real page ----------------------
    const page = await context.newPage();
    await page.goto(`${BASE_URL}/`);
    await expect(page.locator('h1')).toHaveText('Clinic Portal');

    await worker.evaluate(() => {
      (globalThis as unknown as { __htrSeen: unknown[] }).__htrSeen = [];
      chrome.runtime.onMessage.addListener((message) => {
        __htrSeen.push(message);
      });
    });

    // --- fail-closed, part 1: no session, no data -------------------------
    // The content script is loaded, but no capture has been started. It must
    // emit nothing at all — no interaction trail, no blur-region rAF loop.
    await page.click('#open-chart');
    await page.waitForTimeout(750);
    expect(
      await worker.evaluate(() => __htrSeen),
      'the content script emitted data with no capture session running',
    ).toEqual([]);

    // `chrome.tabs.sendMessage` only resolves if a content script is actually
    // listening in that tab — this rejects with "Receiving end does not exist"
    // when the script failed to inject.
    const delivered = await worker.evaluate(async (url) => {
      const [tab] = await chrome.tabs.query({ url });
      if (!tab?.id) return { ok: false, error: 'no matching tab' };
      try {
        await chrome.tabs.sendMessage(tab.id, {
          type: 'htr:capture-start',
          captureId: 'e2e-extension-capture',
          epoch: Date.now(),
          blurSelectors: ['#patient-banner'],
        });
        return { ok: true, error: null };
      } catch (error) {
        return { ok: false, error: String(error) };
      }
    }, `${BASE_URL}/`);
    expect(delivered, 'the content script did not receive the session-start message').toEqual({
      ok: true,
      error: null,
    });

    // A real click in the page must now travel content script -> service
    // worker as a real CaptureEvent built by `startInteractionTrail`.
    await page.click('#open-chart');
    await expect
      .poll(
        async () => {
          const seen = (await worker.evaluate(() => __htrSeen)) as CaptureEvent[];
          return seen.filter((m) => m.kind === 'interaction').length;
        },
        { message: 'waiting for an interaction event from the content script', timeout: 15_000 },
      )
      .toBeGreaterThan(0);

    const interactions = (await worker.evaluate(() => __htrSeen)) as CaptureEvent[];
    const click = interactions.find(
      (m) => m.kind === 'interaction' && 'type' in m.payload && m.payload.type === 'click',
    );
    expect(click, 'no click event arrived from the content script').toBeDefined();
    expect(click?.captureId).toBe('e2e-extension-capture');
    expect(click?.payload).toMatchObject({
      type: 'click',
      targetSelector: '#open-chart',
      targetName: 'Open chart for MRN-4471902',
    });

    // --- fail-closed, part 2: stopping the session stops the data ---------
    await worker.evaluate(async (url) => {
      const [tab] = await chrome.tabs.query({ url });
      await chrome.tabs.sendMessage(tab!.id!, {
        type: 'htr:capture-stop',
        captureId: 'e2e-extension-capture',
      });
    }, `${BASE_URL}/`);

    // The blur-region loop posts every animation frame, and
    // `chrome.runtime.sendMessage` is asynchronous — frames emitted in the
    // moments before the stop arrives are still in flight afterwards. So wait
    // for the stream to fall quiet first; the invariant under test is that
    // emissions *cease and stay ceased*, not that the last in-flight message
    // beats the stop message to the service worker.
    await expect
      .poll(
        async () => {
          const before = (await worker.evaluate(() => __htrSeen)).length;
          await page.waitForTimeout(250);
          const after = (await worker.evaluate(() => __htrSeen)).length;
          return after === before;
        },
        { message: 'the content script never stopped emitting after the stop', timeout: 15_000 },
      )
      .toBe(true);

    // Quiet now — so anything that arrives from here on was emitted with no
    // capture session running.
    await worker.evaluate(() => {
      (globalThis as unknown as { __htrSeen: unknown[] }).__htrSeen = [];
    });
    await page.click('#open-chart');
    await page.waitForTimeout(750);
    expect(
      await worker.evaluate(() => __htrSeen),
      'the content script kept emitting after the capture was stopped',
    ).toEqual([]);

    // --- fail-closed, part 3: the policy gate has no ruleset --------------
    // `rulesetStore` reads exclusively from `chrome.storage.managed`, which an
    // ordinary (non-enterprise-managed) profile leaves empty. That keeps
    // `rulesetStore.current()` at `null`, which is the condition under which
    // `serviceWorker.start()` refuses outright — before any
    // `chrome.debugger.attach` call. See the README for why the toolbar click
    // that would exercise that branch cannot be synthesized here.
    const managed = await worker.evaluate(() => chrome.storage.managed.get(null));
    expect(managed, 'this profile must have no managed ruleset').toEqual({});

    // And the toolbar click really is the only trigger: no popup, no commands.
    const manifest = await worker.evaluate(() => chrome.runtime.getManifest());
    expect(manifest.action?.default_popup).toBeUndefined();
    expect(manifest.commands).toBeUndefined();
  } finally {
    await context.close();
    fs.rmSync(userDataDir, { recursive: true, force: true });
  }
});
