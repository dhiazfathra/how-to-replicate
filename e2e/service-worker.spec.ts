import { expect, test, type CDPSession } from '@playwright/test';
import { createServiceWorker } from '../clients/extension/src/background/service-worker.js';
import type {
  ChromeAdapter,
  DebuggerEvent,
  DebuggerTarget,
} from '../clients/extension/src/lib/chrome-adapter.js';
import { BASE_URL } from './support.js';

/**
 * `createServiceWorker` (production code, `clients/extension/src/background/
 * service-worker.ts`) takes its entire Chrome surface through `ChromeAdapter`
 * — that seam is what lets this spec drive the *real* orchestration logic
 * (ruleset gate, CDP wiring, detach handoff) against a *real* Playwright page
 * and a *real* `chrome.debugger`-equivalent CDP session, without needing an
 * actual loaded MV3 extension.
 *
 * Two scenarios can't be exercised through `clients/extension/e2e/extension.
 * spec.ts` (which loads the real unpacked extension via
 * `launchPersistentContext`): both need a non-empty `chrome.storage.managed`
 * ruleset, and managed policy storage is only populated by an OS-level
 * enterprise policy file — unavailable to the vanilla Chromium binary
 * Playwright drives. `service-worker.test.ts` already covers this at the
 * unit level with an entirely fake `ChromeAdapter`; this spec covers the same
 * production code path against a real browser instead, for the two
 * network/CDP-shaped scenarios that benefit from one.
 */

/** Builds a `ChromeAdapter` whose `debugger` is backed by a real CDP session. */
function adapterWithRealCdp(
  session: CDPSession,
  target: DebuggerTarget,
  managed: Record<string, unknown>,
): ChromeAdapter & { fireDetach: (reason: string) => void } {
  const eventListeners: ((source: DebuggerTarget, message: DebuggerEvent) => void)[] = [];
  const detachListeners: ((source: DebuggerTarget, reason: string) => void)[] = [];

  const FORWARDED = [
    'Network.requestWillBeSent',
    'Network.responseReceived',
    'Network.loadingFinished',
    'Console.messageAdded',
    'Runtime.exceptionThrown',
  ] as const;
  for (const method of FORWARDED) {
    session.on(method, (params) => {
      for (const l of eventListeners) l(target, { method, params });
    });
  }

  return {
    debugger: {
      attach: async () => {
        // The session is already attached by `context.newCDPSession` — this
        // mirrors `chrome.debugger.attach` resolving once a client is live.
      },
      detach: async () => {
        await session.detach().catch(() => undefined);
      },
      sendCommand: (_t, method, params) => session.send(method as never, params as never),
      onEvent: {
        addListener: (l) => eventListeners.push(l),
        removeListener: (l) => {
          const i = eventListeners.indexOf(l);
          if (i >= 0) eventListeners.splice(i, 1);
        },
      },
      onDetach: {
        addListener: (l) => detachListeners.push(l),
        removeListener: (l) => {
          const i = detachListeners.indexOf(l);
          if (i >= 0) detachListeners.splice(i, 1);
        },
      },
    },
    webRequest: {
      onBeforeRequest: { addListener: () => undefined, removeListener: () => undefined },
      onCompleted: { addListener: () => undefined, removeListener: () => undefined },
    },
    storage: {
      managed: { get: () => Promise.resolve(managed) },
      onChanged: { addListener: () => undefined, removeListener: () => undefined },
    },
    offscreen: {
      hasDocument: () => Promise.resolve(true),
      createDocument: () => Promise.resolve(),
      closeDocument: () => Promise.resolve(),
    },
    runtime: {
      sendMessage: () => Promise.resolve(undefined),
      onMessage: { addListener: () => undefined, removeListener: () => undefined },
      getURL: (path) => `chrome-extension://ext/${path}`,
    },
    action: {
      setBadgeText: () => Promise.resolve(undefined),
      onClicked: { addListener: () => undefined, removeListener: () => undefined },
    },
    tabs: {
      captureVisibleTab: () => Promise.resolve('data:image/png;base64,'),
      sendMessage: () => Promise.resolve(undefined),
    },
    // `chrome.debugger.onDetach` is fired by Chrome itself in production, with
    // a reason Chrome chose (`target_closed`, `canceled_by_user`, opening
    // DevTools, ...). Nothing outside a real loaded extension can make Chrome
    // emit that event, so this test fires it explicitly — after really
    // detaching the underlying CDP session above — and lets the *real*
    // `onDetachListener` built in `service-worker.ts` do the rest.
    fireDetach(reason: string) {
      for (const l of detachListeners) l(target, reason);
    },
  };
}

const target: DebuggerTarget = { tabId: 1 };

test('off-allow-list refusal: an origin not on the ruleset never gets a debugger attach', async ({
  page,
  context,
}) => {
  await page.goto(`${BASE_URL}/`);
  const session = await context.newCDPSession(page);
  let attachCalled = false;

  const chromeApi = adapterWithRealCdp(session, target, {
    ruleset: {
      version: '1',
      rules: [{ id: 'allow', class: 'origin-allow', origins: ['https://not-this-origin.example'] }],
    },
  });
  const realAttach = chromeApi.debugger.attach;
  chromeApi.debugger.attach = async (t, v) => {
    attachCalled = true;
    return realAttach(t, v);
  };

  const worker = createServiceWorker(chromeApi);
  await worker.rulesetStore.load();

  const active = await worker.start(BASE_URL, target);

  expect(active, 'an off-allow-list origin must not be allowed to start a capture').toBeNull();
  expect(attachCalled, 'no chrome.debugger.attach call may happen for a refused origin').toBe(false);
});

test('CDP-detach handoff: losing the debugger mid-capture degrades fidelity and keeps capturing', async ({
  page,
  context,
}) => {
  await page.goto(`${BASE_URL}/`);
  const session = await context.newCDPSession(page);

  const chromeApi = adapterWithRealCdp(session, target, {
    ruleset: { version: '1', rules: [{ id: 'allow', class: 'origin-allow', origins: [BASE_URL] }] },
  });

  const worker = createServiceWorker(chromeApi);
  await worker.rulesetStore.load();

  const active = await worker.start(BASE_URL, target);
  expect(active, 'an allow-listed origin must be able to start a capture').not.toBeNull();
  expect(active!.capture.fidelity).toBe('full');

  // A real detach of the real CDP session — e.g. what happens when DevTools
  // steals the single `chrome.debugger` client Chrome allows per tab.
  await session.detach();
  chromeApi.fireDetach('canceled_by_user');

  expect(active!.capture.fidelity, 'losing the debugger mid-capture must degrade fidelity').toBe(
    'degraded',
  );

  const events = active!.buffer.events();
  expect(
    events.some((e) => e.kind === 'lifecycle'),
    'the detach must be recorded as a lifecycle event, not silently absorbed',
  ).toBe(true);
});
