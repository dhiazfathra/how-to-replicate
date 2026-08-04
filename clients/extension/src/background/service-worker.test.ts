import 'fake-indexeddb/auto';
import { describe, expect, it, vi } from 'vitest';
import { openCaptureDb, CaptureRepository } from '@htr/capture-core';
import { encodeChunk } from '../lib/chunk-codec.js';
import { createServiceWorker, wireServiceWorker } from './service-worker.js';
import { MANAGED_RULESET_KEY } from './ruleset-store.js';
import { CAPTURE_START_MESSAGE_TYPE, CAPTURE_STOP_MESSAGE_TYPE } from '../content/session.js';
import type {
  ChromeAdapter,
  DebuggerEvent,
  DebuggerTarget,
} from '../lib/chrome-adapter.js';

const allowedRuleset = {
  version: '1',
  rules: [
    { id: 'allow', class: 'origin-allow', origins: ['https://allowed.test'] },
    { id: 'blur-ssn', class: 'video-blur', selector: '.ssn' },
  ],
};

function fakeAdapter(managed: Record<string, unknown> = {}): ChromeAdapter & {
  fireDebuggerEvent(source: DebuggerTarget, message: DebuggerEvent): void;
  fireDetach(source: DebuggerTarget, reason: string): void;
  fireMessage(message: unknown, sendResponse?: (r?: unknown) => void): void;
  detachListenerCount(): number;
  messageListenerCount(): number;
} {
  const eventListeners: ((source: DebuggerTarget, message: DebuggerEvent) => void)[] = [];
  const detachListeners: ((source: DebuggerTarget, reason: string) => void)[] = [];
  const messageListeners: ((message: unknown, sender: unknown, sendResponse: (r?: unknown) => void) => void)[] = [];
  let offscreenExists = false;

  return {
    debugger: {
      attach: vi.fn().mockResolvedValue(undefined),
      detach: vi.fn().mockResolvedValue(undefined),
      sendCommand: vi.fn().mockResolvedValue(undefined),
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
      onBeforeRequest: { addListener: vi.fn(), removeListener: vi.fn() },
      onCompleted: { addListener: vi.fn(), removeListener: vi.fn() },
    },
    storage: {
      managed: { get: () => Promise.resolve(managed) },
      onChanged: { addListener: vi.fn(), removeListener: vi.fn() },
    },
    offscreen: {
      hasDocument: () => Promise.resolve(offscreenExists),
      createDocument: () => {
        offscreenExists = true;
        return Promise.resolve();
      },
      closeDocument: () => {
        offscreenExists = false;
        return Promise.resolve();
      },
    },
    runtime: {
      sendMessage: vi.fn().mockResolvedValue(undefined),
      onMessage: {
        addListener: (l) => messageListeners.push(l),
        removeListener: (l) => {
          const i = messageListeners.indexOf(l);
          if (i >= 0) messageListeners.splice(i, 1);
        },
      },
      getURL: (path) => `chrome-extension://ext/${path}`,
    },
    action: {
      setBadgeText: vi.fn().mockResolvedValue(undefined),
      onClicked: { addListener: vi.fn(), removeListener: vi.fn() },
    },
    tabs: {
      captureVisibleTab: vi.fn().mockResolvedValue('data:image/png;base64,'),
      sendMessage: vi.fn().mockResolvedValue(undefined),
    },
    fireDebuggerEvent(source, message) {
      for (const l of eventListeners) l(source, message);
    },
    fireDetach(source, reason) {
      for (const l of detachListeners) l(source, reason);
    },
    fireMessage(message, sendResponse = () => undefined) {
      for (const l of messageListeners) l(message, undefined, sendResponse);
    },
    detachListenerCount: () => detachListeners.length,
    messageListenerCount: () => messageListeners.length,
  };
}

const target = { tabId: 1 };

describe('createServiceWorker', () => {
  it('refuses to start a capture when no ruleset has loaded', async () => {
    const chromeApi = fakeAdapter();
    const worker = createServiceWorker(chromeApi);
    const active = await worker.start('https://allowed.test', target);
    expect(active).toBeNull();
    expect(chromeApi.debugger.attach).not.toHaveBeenCalled();
  });

  it('refuses to start a capture on an off-allow-list origin — no debugger attach at all', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();

    const active = await worker.start('https://evil.test', target);

    expect(active).toBeNull();
    expect(chromeApi.debugger.attach).not.toHaveBeenCalled();
  });

  it('starts a capture on an allow-listed origin', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();

    const active = await worker.start('https://allowed.test', target);

    expect(active).not.toBeNull();
    expect(chromeApi.debugger.attach).toHaveBeenCalledWith(target, '1.3');
    expect(active!.capture.state).toBe('recording');
    expect(active!.capture.fidelity).toBe('full');
  });

  it('ingests mapped CDP events into the Instant Replay buffer', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);

    chromeApi.fireDebuggerEvent(target, {
      method: 'Console.messageAdded',
      params: { message: { level: 'log', text: 'hi' } },
    });

    expect(active!.buffer.events()).toHaveLength(1);
  });

  it('on debugger detach, degrades fidelity, records the handoff, and falls back to webRequest', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);

    chromeApi.fireDetach(target, 'target_closed');

    expect(active!.capture.fidelity).toBe('degraded');
    expect(active!.fallback).not.toBeNull();
    const events = active!.buffer.events();
    expect(events.some((e) => e.kind === 'lifecycle')).toBe(true);
    expect(chromeApi.action.setBadgeText).toHaveBeenCalledWith({ text: 'DEG', tabId: 1 });
  });

  it('ignores a detach for a different tab', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);

    chromeApi.fireDetach({ tabId: 999 }, 'target_closed');

    expect(active!.capture.fidelity).toBe('full');
    expect(active!.fallback).toBeNull();
  });

  it('ignores a second detach once fallback is already active', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);

    chromeApi.fireDetach(target, 'target_closed');
    const firstFallback = active!.fallback;
    chromeApi.fireDetach(target, 'target_closed_again');
    expect(active!.fallback).toBe(firstFallback);
  });

  it('stop() tears down fallback (if any) and detaches the debugger', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);
    chromeApi.fireDetach(target, 'target_closed');

    const result = await worker.stop(active!);

    expect(chromeApi.debugger.detach).toHaveBeenCalledWith(target);
    expect(result.id).toBe(active!.capture.id);
  });

  it('stop() tolerates a debugger.detach() rejection', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    (chromeApi.debugger.detach as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('gone'));
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);

    await expect(worker.stop(active!)).resolves.toMatchObject({ id: active!.capture.id });
  });

  it('routes htr:video-chunk / htr:screenshot / htr:lifecycle / htr:degraded messages into the active capture', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);
    const captureId = active!.capture.id;
    const data = new Uint8Array([1]);
    const encoded = await encodeChunk(data);

    chromeApi.fireMessage({ type: 'htr:video-chunk', captureId, data: encoded });
    chromeApi.fireMessage({ type: 'htr:screenshot', captureId, dataUrl: 'data:image/png;base64,x' });
    chromeApi.fireMessage({ type: 'htr:lifecycle', captureId, transition: 'video->screenshot', detail: 'budget' });
    chromeApi.fireMessage({ type: 'htr:degraded', captureId });

    const chunks = active!.buffer.videoChunks();
    expect(chunks).toHaveLength(1);
    expect(chunks[0]).toMatchObject({ data });

    const screenshots = active!.buffer.screenshots();
    expect(screenshots).toHaveLength(1);
    expect(screenshots[0]).toMatchObject({ dataUrl: 'data:image/png;base64,x' });
    expect(active!.buffer.events().some((e) => e.kind === 'lifecycle')).toBe(true);
    expect(active!.capture.fidelity).toBe('degraded');
  });

  it('ignores htr:* messages addressed to a different captureId', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);

    chromeApi.fireMessage({ type: 'htr:degraded', captureId: 'some-other-capture' });

    expect(active!.capture.fidelity).toBe('full');
  });

  it('answers htr:capture-screenshot-request via chrome.tabs, since the offscreen document cannot call it itself', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);
    const sendResponse = vi.fn();

    chromeApi.fireMessage({ type: 'htr:capture-screenshot-request', captureId: active!.capture.id }, sendResponse);
    await vi.waitFor(() => expect(sendResponse).toHaveBeenCalledWith('data:image/png;base64,'));

    expect(chromeApi.tabs.captureVisibleTab).toHaveBeenCalled();
  });

  it('ensureOffscreenDocument creates the document with the captureId in the URL, only when none exists', async () => {
    const chromeApi = fakeAdapter();
    const worker = createServiceWorker(chromeApi);
    const createSpy = vi.spyOn(chromeApi.offscreen, 'createDocument');

    await worker.ensureOffscreenDocument('cap-42');
    expect(createSpy).toHaveBeenCalledTimes(1);
    expect(createSpy.mock.calls[0]![0].url).toBe('chrome-extension://ext/offscreen.html?captureId=cap-42');

    await worker.ensureOffscreenDocument('cap-42');
    expect(createSpy).toHaveBeenCalledTimes(1);
  });

  it('start() creates the offscreen document carrying the real captureId and pushes start session control to the content script', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const createSpy = vi.spyOn(chromeApi.offscreen, 'createDocument');

    const active = await worker.start('https://allowed.test', target);

    expect(createSpy).toHaveBeenCalledWith(
      expect.objectContaining({ url: `chrome-extension://ext/offscreen.html?captureId=${active!.capture.id}` }),
    );
    expect(chromeApi.tabs.sendMessage).toHaveBeenCalledWith(target.tabId, {
      type: CAPTURE_START_MESSAGE_TYPE,
      captureId: active!.capture.id,
      epoch: active!.capture.epoch,
      blurSelectors: ['.ssn'],
    });
  });

  it('stop() pushes stop session control, removes fallback/message/detach listeners, and finalizes + persists the capture', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);
    const captureId = active!.capture.id;

    expect(chromeApi.detachListenerCount()).toBe(1);
    const messageListenersAfterStart = chromeApi.messageListenerCount();
    expect(messageListenersAfterStart).toBeGreaterThanOrEqual(2);

    const result = await worker.stop(active!);

    expect(chromeApi.tabs.sendMessage).toHaveBeenCalledWith(target.tabId, {
      type: CAPTURE_STOP_MESSAGE_TYPE,
      captureId,
    });
    expect(chromeApi.detachListenerCount()).toBe(0);
    expect(chromeApi.messageListenerCount()).toBe(messageListenersAfterStart - 2);

    expect(result.state).toBe('ready');

    const db = await openCaptureDb();
    const repo = new CaptureRepository(db);
    const persisted = await repo.getCapture(captureId);
    expect(persisted?.state).toBe('ready');
    const events = await repo.readEvents(captureId);
    expect(events.some((e) => e.kind === 'lifecycle' && (e.payload as { transition?: string }).transition === 'recording->redacting')).toBe(true);
  });

  it('stop() closes the offscreen document so a second capture gets a fresh one with the new captureId (not the stale reused one)', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const createSpy = vi.spyOn(chromeApi.offscreen, 'createDocument');

    const first = await worker.start('https://allowed.test', target);
    await worker.stop(first!);

    const second = await worker.start('https://allowed.test', target);

    expect(second!.capture.id).not.toBe(first!.capture.id);
    expect(createSpy).toHaveBeenCalledTimes(2);
    expect(createSpy.mock.calls[1]![0].url).toBe(
      `chrome-extension://ext/offscreen.html?captureId=${second!.capture.id}`,
    );
  });

  it('a stopped capture no longer answers screenshot requests or debugger detach events', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = createServiceWorker(chromeApi);
    await worker.rulesetStore.load();
    const active = await worker.start('https://allowed.test', target);
    await worker.stop(active!);

    const sendResponse = vi.fn();
    chromeApi.fireMessage({ type: 'htr:capture-screenshot-request', captureId: active!.capture.id }, sendResponse);
    expect(chromeApi.tabs.captureVisibleTab).not.toHaveBeenCalled();

    chromeApi.fireDetach(target, 'target_closed');
    expect(active!.fallback).toBeNull();
  });
});

describe('wireServiceWorker', () => {
  it('starts the ruleset refresh loop and loads once', async () => {
    const chromeApi = fakeAdapter({ [MANAGED_RULESET_KEY]: allowedRuleset });
    const worker = wireServiceWorker(chromeApi);
    await vi.waitFor(() => expect(worker.rulesetStore.current()).not.toBeNull());
    worker.rulesetStore.stopRefresh();
  });
});
