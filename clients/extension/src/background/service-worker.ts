import {
  CaptureRepository,
  createClock,
  createInstantReplay,
  createRedactor,
  createRingBuffer,
  finalizeCapture,
  newId,
  openCaptureDb,
  transition,
  type Capture,
  type CaptureEvent,
  type Clock,
  type InstantReplay,
  type VideoBlurRule,
} from '@htr/capture-core';
import type { ChromeAdapter, DebuggerTarget } from '../lib/chrome-adapter.js';
import { createRulesetStore, type RulesetStore } from './ruleset-store.js';
import { attachCdp, type CdpSession } from './cdp.js';
import { buildDegradedHandoffEvent, startFallback, type FallbackSession } from './fallback.js';
import { createOffscreenRelayListener, createScreenshotRequestListener } from './offscreen-relay.js';
import { CAPTURE_START_MESSAGE_TYPE, CAPTURE_STOP_MESSAGE_TYPE } from '../content/session.js';

const OFFSCREEN_URL = 'offscreen.html';
const OFFSCREEN_REASONS = ['DISPLAY_MEDIA'];
const OFFSCREEN_JUSTIFICATION = 'Records and blurs capture video off the visible tab.';

type OnDetachListener = (source: DebuggerTarget, reason: string) => void;
type OnMessageListener = (message: unknown, sender: unknown, sendResponse: (r?: unknown) => void) => boolean | void;

export type ActiveCapture = {
  capture: Capture;
  buffer: InstantReplay;
  clock: Clock;
  target: DebuggerTarget;
  cdp: CdpSession;
  fallback: FallbackSession | null;
  /** Kept so `stop()` can remove exactly the listeners `start()` added (Important-5 fix). */
  onDetachListener: OnDetachListener;
  onOffscreenMessageListener: OnMessageListener;
  onScreenshotRequestListener: OnMessageListener;
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
  ensureOffscreenDocument(captureId: string): Promise<void>;
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
  // Opened lazily (only once `stop()` actually needs to persist a capture)
  // and reused across captures — `openCaptureDb` is idempotent for a given
  // name, and the viewer opens this exact same default-named database.
  let repoPromise: Promise<CaptureRepository> | null = null;
  function getRepo(): Promise<CaptureRepository> {
    repoPromise ??= openCaptureDb().then((db) => new CaptureRepository(db));
    return repoPromise;
  }

  async function ensureOffscreenDocument(captureId: string): Promise<void> {
    const exists = await chromeApi.offscreen.hasDocument();
    if (exists) return;
    const url = new URL(chromeApi.runtime.getURL(OFFSCREEN_URL));
    url.searchParams.set('captureId', captureId);
    await chromeApi.offscreen.createDocument({
      url: url.toString(),
      reasons: OFFSCREEN_REASONS,
      justification: OFFSCREEN_JUSTIFICATION,
    });
  }

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

      const onDetachListener: OnDetachListener = (source, reason) => {
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
      };
      chromeApi.debugger.onDetach.addListener(onDetachListener);

      const onOffscreenMessageListener: OnMessageListener = createOffscreenRelayListener(
        capture.id,
        buffer,
        clock,
        () => {
          active.capture = { ...active.capture, fidelity: 'degraded' };
        },
      );
      chromeApi.runtime.onMessage.addListener(onOffscreenMessageListener);

      // Offscreen documents can't call `chrome.tabs.*` directly (MV3) — the
      // periodic-screenshot floor (invariant-2 fallback) relays its capture
      // request here, where `chrome.tabs` is actually available.
      const onScreenshotRequestListener: OnMessageListener = createScreenshotRequestListener(
        capture.id,
        chromeApi.tabs,
      );
      chromeApi.runtime.onMessage.addListener(onScreenshotRequestListener);

      const active: ActiveCapture = {
        capture,
        buffer,
        clock,
        target,
        cdp,
        fallback: null,
        onDetachListener,
        onOffscreenMessageListener,
        onScreenshotRequestListener,
      };

      await ensureOffscreenDocument(capture.id);

      // Push the shared captureId/epoch/blur selectors to the tab's content
      // script — it cannot derive any of these on its own (Critical 3/4 fix).
      const blurSelectors = ruleset.rules
        .filter((rule): rule is VideoBlurRule => rule.class === 'video-blur')
        .map((rule) => rule.selector);
      await chromeApi.tabs
        .sendMessage(target.tabId, {
          type: CAPTURE_START_MESSAGE_TYPE,
          captureId: capture.id,
          epoch: clock.epoch,
          blurSelectors,
        })
        .catch(() => undefined);

      return active;
    },

    async stop(active: ActiveCapture): Promise<Capture> {
      active.fallback?.stop();
      await active.cdp.detach().catch(() => undefined);
      chromeApi.debugger.onDetach.removeListener(active.onDetachListener);
      chromeApi.runtime.onMessage.removeListener(active.onOffscreenMessageListener);
      chromeApi.runtime.onMessage.removeListener(active.onScreenshotRequestListener);
      await chromeApi.tabs
        .sendMessage(active.target.tabId, {
          type: CAPTURE_STOP_MESSAGE_TYPE,
          captureId: active.capture.id,
        })
        .catch(() => undefined);

      const { capture: redacting, event: redactingEvent } = transition(
        active.capture,
        'redacting',
        null,
        active.clock.now(),
      );
      active.buffer.ingest(redactingEvent);
      const { capture: composing, event: composingEvent } = transition(
        redacting,
        'composing',
        null,
        active.clock.now(),
      );
      active.buffer.ingest(composingEvent);

      const repo = await getRepo();
      const finalized = await finalizeCapture({
        capture: composing,
        buffer: active.buffer,
        repo,
        clock: active.clock,
      });
      active.capture = finalized;
      return finalized;
    },

    ensureOffscreenDocument,
  };
}

/** Real entry point: wires the service worker against the ambient `chrome` global. */
export function wireServiceWorker(chromeApi: ChromeAdapter): ServiceWorker {
  const worker = createServiceWorker(chromeApi);
  worker.rulesetStore.startRefresh();
  void worker.rulesetStore.load();
  return worker;
}
