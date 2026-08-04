import {
  createClock,
  createInstantReplay,
  createRedactor,
  createRingBuffer,
  newId,
  type Capture,
  type CaptureEvent,
  type Clock,
  type InstantReplay,
} from '@htr/capture-core';
import type { ChromeAdapter, DebuggerTarget } from '../lib/chrome-adapter.js';
import { createRulesetStore, type RulesetStore } from './ruleset-store.js';
import { attachCdp, type CdpSession } from './cdp.js';
import { buildDegradedHandoffEvent, startFallback, type FallbackSession } from './fallback.js';

const OFFSCREEN_URL = 'offscreen.html';
const OFFSCREEN_REASONS = ['DISPLAY_MEDIA'];
const OFFSCREEN_JUSTIFICATION = 'Records and blurs capture video off the visible tab.';

export type ActiveCapture = {
  capture: Capture;
  buffer: InstantReplay;
  clock: Clock;
  target: DebuggerTarget;
  cdp: CdpSession;
  fallback: FallbackSession | null;
};

export type ServiceWorker = {
  rulesetStore: RulesetStore;
  /**
   * Start a capture for `origin`/`target`, or return `null` if `origin` is
   * off the policy allow-list. Refusal is total: no `chrome.debugger.attach`
   * call happens at all, not even for a screenshot or metadata-only capture
   * — an unlisted origin gets zero bytes of anything.
   */
  start(origin: string, target: DebuggerTarget): Promise<ActiveCapture | null>;
  stop(active: ActiveCapture): Promise<Capture>;
  ensureOffscreenDocument(): Promise<void>;
};

function newCapture(clock: Clock): Capture {
  return {
    id: newId(),
    workspaceId: null,
    projectId: null,
    source: 'extension',
    state: 'recording',
    fidelity: 'full',
    createdAt: new Date().toISOString(),
    epoch: clock.epoch,
    env: {
      userAgent: '',
      platform: '',
      viewport: { w: 0, h: 0 },
      devicePixelRatio: 1,
      locale: '',
      timezone: '',
      url: '',
    },
    metadata: {},
    doc: null,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
  };
}

/**
 * Wires MV3 service-worker orchestration on top of `capture-core`'s state
 * machine and buffers. Takes its Chrome surface entirely through `chrome` —
 * the injected adapter — so it never touches the ambient `chrome` global,
 * which is what lets tests stub the boundary instead of a real browser.
 */
export function createServiceWorker(chromeApi: ChromeAdapter): ServiceWorker {
  const rulesetStore = createRulesetStore(chromeApi.storage);

  return {
    rulesetStore,

    async start(origin: string, target: DebuggerTarget): Promise<ActiveCapture | null> {
      const ruleset = rulesetStore.current();
      if (!ruleset) return null;
      const redactor = createRedactor(ruleset);
      if (!redactor.isOriginAllowed(origin)) return null;

      const clock = createClock();
      const capture = newCapture(clock);
      const buffer = createInstantReplay({
        redactor,
        ring: createRingBuffer<CaptureEvent>(),
        clock,
      });

      const onEvent = (event: CaptureEvent): void => buffer.ingest(event);
      const cdp = await attachCdp(chromeApi.debugger, target, capture.id, clock, onEvent);

      const active: ActiveCapture = { capture, buffer, clock, target, cdp, fallback: null };

      chromeApi.debugger.onDetach.addListener((source, reason) => {
        if (source.tabId !== target.tabId || active.fallback) return;
        active.capture = { ...active.capture, fidelity: 'degraded' };
        buffer.ingest(buildDegradedHandoffEvent(capture.id, clock, reason));
        active.fallback = startFallback(
          chromeApi.webRequest,
          chromeApi.action,
          target,
          capture.id,
          clock,
          onEvent,
        );
      });

      return active;
    },

    async stop(active: ActiveCapture): Promise<Capture> {
      active.fallback?.stop();
      await active.cdp.detach().catch(() => undefined);
      return active.capture;
    },

    async ensureOffscreenDocument(): Promise<void> {
      const exists = await chromeApi.offscreen.hasDocument();
      if (exists) return;
      await chromeApi.offscreen.createDocument({
        url: chromeApi.runtime.getURL(OFFSCREEN_URL),
        reasons: OFFSCREEN_REASONS,
        justification: OFFSCREEN_JUSTIFICATION,
      });
    },
  };
}

/** Real entry point: wires the service worker against the ambient `chrome` global. */
export function wireServiceWorker(chromeApi: ChromeAdapter): ServiceWorker {
  const worker = createServiceWorker(chromeApi);
  worker.rulesetStore.startRefresh();
  void worker.rulesetStore.load();
  return worker;
}
