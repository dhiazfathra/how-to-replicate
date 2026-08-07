import { expect, type BrowserContext, type CDPSession, type Page } from '@playwright/test';
import type { CaptureEvent } from '@htr/capture-core';
// Side-effect import: pulls in the `window.__htr` global augmentation so
// `page.evaluate` callbacks in the specs are type-checked against the harness.
import './harness/api.js';

export const PORT = Number(process.env.HTR_E2E_PORT ?? 5178);
export const BASE_URL = `http://127.0.0.1:${PORT}`;

/**
 * The synthetic PHI planted in the demo app's DOM, console output, request
 * URL and request body. Every value here must be absent from every persisted
 * capture event — that assertion is the redaction proof.
 */
export const PHI = {
  mrn: 'MRN-4471902',
  nik: '3174091203880007',
  email: 'budi.santoso@clinic.example',
  dob: '1988-03-12',
  phone: '+6281234567890',
} as const;

/** Not PHI, but a credential the `header` rule must strip. */
export const AUTH_TOKEN = 'clinic-session-token-abc123';

/** The CDP events `mapCdpEvent` knows how to map. */
const FORWARDED = [
  'Network.requestWillBeSent',
  'Network.responseReceived',
  'Network.loadingFinished',
  'Console.messageAdded',
  'Runtime.exceptionThrown',
] as const;

export type CdpBridge = {
  session: CDPSession;
  /** Resolves once every CDP message received so far has reached the harness. */
  flush(): Promise<void>;
};

/**
 * Attach a real CDP session to `page` and forward the raw protocol messages
 * into the in-page harness, where the extension's own `mapCdpEvent` turns
 * them into `CaptureEvent`s.
 *
 * This is the seam where Playwright stands in for `chrome.debugger`: the
 * attach, the domain enables and the protocol traffic are all real, and the
 * mapping/redaction/buffering on the other side is real product code. The
 * forwards are chained through a single promise so the harness sees messages
 * in protocol order (`mapCdpEvent` folds `requestWillBeSent` and
 * `loadingFinished` into one network event via a pending-request map, which
 * an out-of-order delivery would break).
 */
export async function bridgeCdpToHarness(
  context: BrowserContext,
  page: Page,
): Promise<CdpBridge> {
  const session = await context.newCDPSession(page);
  await session.send('Network.enable', { maxPostDataSize: 65_536 });
  await session.send('Runtime.enable');
  await session.send('Console.enable');

  let chain: Promise<unknown> = Promise.resolve();
  const forward = (method: string, params: unknown): void => {
    chain = chain
      .then(() =>
        page.evaluate(
          ([m, p]) => {
            window.__htr.pushCdp({ method: m, params: p as Record<string, unknown> });
          },
          [method, params] as [string, unknown],
        ),
      )
      // A forward can lose its race with a page teardown; that is not a
      // capture failure, and swallowing it must not swallow a real one — the
      // specs assert on the events that did arrive, never on the absence of
      // an error here.
      .catch(() => undefined);
  };

  session.on('Network.requestWillBeSent', (p) => {
    forward(FORWARDED[0], p);
  });
  session.on('Network.responseReceived', (p) => {
    forward(FORWARDED[1], p);
  });
  session.on('Network.loadingFinished', (p) => {
    forward(FORWARDED[2], p);
  });
  session.on('Console.messageAdded', (p) => {
    forward(FORWARDED[3], p);
  });
  session.on('Runtime.exceptionThrown', (p) => {
    forward(FORWARDED[4], p);
  });

  return {
    session,
    async flush() {
      await chain;
    },
  };
}

/** Wait until the live (already-redacted) buffer holds an event matching `predicate`. */
export async function waitForBufferedEvent(
  page: Page,
  bridge: CdpBridge,
  label: string,
  predicate: (event: CaptureEvent) => boolean,
): Promise<void> {
  await expect
    .poll(
      async () => {
        await bridge.flush();
        const events = await page.evaluate(() => window.__htr.events());
        return events.some(predicate);
      },
      { message: `waiting for a buffered ${label} event`, timeout: 15_000 },
    )
    .toBe(true);
}

/**
 * Drive the demo app the way a person reporting a bug would: fill the lookup
 * form, submit it, open a chart, hit the failing endpoint, trigger the crash,
 * then navigate. Every step is a real Playwright input event against a real
 * DOM node — nothing here synthesizes a `CaptureEvent`.
 */
export async function reproduceTheBug(
  page: Page,
  bridge: CdpBridge,
  options: { legacyChart: boolean },
): Promise<void> {
  await page.fill('#mrn-field', PHI.mrn);
  await page.fill('#email-field', PHI.email);
  await page.click('#submit-lookup');
  await expect(page.locator('#log')).toHaveText('Lookup submitted.');

  await page.click('#open-chart');
  await expect(page.locator('#log')).toHaveText('Opening chart…');

  await page.click(options.legacyChart ? '#load-chart-legacy' : '#load-chart');
  await expect(page.locator('#log')).toHaveText('Chart request failed: HTTP 500');

  if (options.legacyChart) {
    // The legacy body shape defeats the `/patient/mrn` pointer, so the engine
    // fails closed: the request never becomes an event at all, it becomes a
    // withheld count.
    await expect
      .poll(
        async () => {
          await bridge.flush();
          return (await page.evaluate(() => window.__htr.stats())).withheld;
        },
        { message: 'waiting for the legacy request to be dropped', timeout: 15_000 },
      )
      .toBeGreaterThan(0);
  } else {
    await waitForBufferedEvent(page, bridge, 'network', (event) => event.kind === 'network');
  }

  await page.click('#crash');
  await waitForBufferedEvent(
    page,
    bridge,
    'console error',
    (event) => event.kind === 'console' && 'level' in event.payload && event.payload.level === 'error',
  );

  await page.click('#nav-billing');
  await expect(page.locator('#route')).toHaveText('Route: #/billing');

  await waitForBufferedEvent(page, bridge, 'navigation', (event) => event.kind === 'navigation');
  await bridge.flush();
}
