import { describe, expect, it, vi } from 'vitest';
import { createServiceWorker, wireServiceWorker } from './service-worker.js';
import { MANAGED_RULESET_KEY } from './ruleset-store.js';
import type {
  ChromeAdapter,
  DebuggerEvent,
  DebuggerTarget,
} from '../lib/chrome-adapter.js';

const allowedRuleset = {
  version: '1',
  rules: [{ id: 'allow', class: 'origin-allow', origins: ['https://allowed.test'] }],
};

function fakeAdapter(managed: Record<string, unknown> = {}): ChromeAdapter & {
  fireDebuggerEvent(source: DebuggerTarget, message: DebuggerEvent): void;
  fireDetach(source: DebuggerTarget, reason: string): void;
} {
  const eventListeners: ((source: DebuggerTarget, message: DebuggerEvent) => void)[] = [];
  const detachListeners: ((source: DebuggerTarget, reason: string) => void)[] = [];
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
        removeListener: () => undefined,
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
      closeDocument: () => Promise.resolve(),
    },
    runtime: {
      sendMessage: vi.fn().mockResolvedValue(undefined),
      onMessage: { addListener: vi.fn(), removeListener: vi.fn() },
      getURL: (path) => `chrome-extension://ext/${path}`,
    },
    action: {
      setBadgeText: vi.fn().mockResolvedValue(undefined),
    },
    tabs: {
      captureVisibleTab: vi.fn().mockResolvedValue('data:image/png;base64,'),
    },
    fireDebuggerEvent(source, message) {
      for (const l of eventListeners) l(source, message);
    },
    fireDetach(source, reason) {
      for (const l of detachListeners) l(source, reason);
    },
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

  it('ensureOffscreenDocument creates the document only when none exists', async () => {
    const chromeApi = fakeAdapter();
    const worker = createServiceWorker(chromeApi);
    const createSpy = vi.spyOn(chromeApi.offscreen, 'createDocument');

    await worker.ensureOffscreenDocument();
    expect(createSpy).toHaveBeenCalledTimes(1);

    await worker.ensureOffscreenDocument();
    expect(createSpy).toHaveBeenCalledTimes(1);
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
