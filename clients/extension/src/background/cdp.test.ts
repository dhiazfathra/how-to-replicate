import { describe, expect, it, vi } from 'vitest';
import type { CaptureEvent, Clock, ConsolePayload } from '@htr/capture-core';
import { attachCdp, mapCdpEvent } from './cdp.js';
import type { ChromeDebugger, DebuggerEvent, DebuggerTarget } from '../lib/chrome-adapter.js';

function fakeClock(t = 0): Clock {
  return { epoch: 0, now: () => t };
}

function fakeDebugger(): ChromeDebugger & {
  emit(source: DebuggerTarget, message: DebuggerEvent): void;
  detachSubscribers: ((source: DebuggerTarget, reason: string) => void)[];
} {
  const eventListeners: ((source: DebuggerTarget, message: DebuggerEvent) => void)[] = [];
  const detachSubscribers: ((source: DebuggerTarget, reason: string) => void)[] = [];
  return {
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
      addListener: (l) => detachSubscribers.push(l),
      removeListener: () => undefined,
    },
    emit(source, message) {
      for (const l of eventListeners) l(source, message);
    },
    detachSubscribers,
  };
}

describe('attachCdp', () => {
  const target = { tabId: 1 };

  it('attaches and enables Network/Console/Runtime', async () => {
    const dbg = fakeDebugger();
    await attachCdp(dbg, target, 'cap-1', fakeClock(), () => undefined);
    expect(dbg.attach).toHaveBeenCalledWith(target, '1.3');
    expect(dbg.sendCommand).toHaveBeenCalledWith(target, 'Network.enable');
    expect(dbg.sendCommand).toHaveBeenCalledWith(target, 'Console.enable');
    expect(dbg.sendCommand).toHaveBeenCalledWith(target, 'Runtime.enable');
  });

  it('ignores events from another tab', async () => {
    const dbg = fakeDebugger();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    await attachCdp(dbg, target, 'cap-1', fakeClock(), onEvent);
    dbg.emit({ tabId: 999 }, { method: 'Console.messageAdded', params: { message: { level: 'log', text: 'x' } } });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it('maps a full network request/response/finished sequence into one event', async () => {
    const dbg = fakeDebugger();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    await attachCdp(dbg, target, 'cap-1', fakeClock(5), onEvent);

    dbg.emit(target, {
      method: 'Network.requestWillBeSent',
      params: {
        requestId: 'r1',
        request: { url: 'https://x.test/a', method: 'GET', headers: { A: '1' }, postData: 'body' },
      },
    });
    expect(onEvent).not.toHaveBeenCalled();

    dbg.emit(target, {
      method: 'Network.responseReceived',
      params: { requestId: 'r1', response: { status: 200, headers: { B: '2' } } },
    });
    expect(onEvent).not.toHaveBeenCalled();

    dbg.emit(target, {
      method: 'Network.loadingFinished',
      params: { requestId: 'r1', encodedDataLength: 42 },
    });

    expect(onEvent).toHaveBeenCalledTimes(1);
    const event = onEvent.mock.calls[0]![0];
    expect(event.kind).toBe('network');
    expect(event.t).toBe(5);
    expect(event.payload).toMatchObject({
      method: 'GET',
      url: 'https://x.test/a',
      status: 200,
      requestHeaders: { A: '1' },
      responseHeaders: { B: '2' },
      requestBody: 'body',
      sizeBytes: 42,
    });
  });

  it('drops responseReceived/loadingFinished for an unknown requestId', async () => {
    const dbg = fakeDebugger();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    await attachCdp(dbg, target, 'cap-1', fakeClock(), onEvent);
    dbg.emit(target, { method: 'Network.responseReceived', params: { requestId: 'ghost', response: { status: 1, headers: {} } } });
    dbg.emit(target, { method: 'Network.loadingFinished', params: { requestId: 'ghost' } });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it('maps Console.messageAdded to a console event', async () => {
    const dbg = fakeDebugger();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    await attachCdp(dbg, target, 'cap-1', fakeClock(), onEvent);
    dbg.emit(target, {
      method: 'Console.messageAdded',
      params: { message: { level: 'warn', text: 'oops', stackTrace: { callFrames: [] } } },
    });
    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(onEvent.mock.calls[0]![0].payload).toMatchObject({ level: 'warn', text: 'oops' });
  });

  it('normalizes an unknown console level to log', async () => {
    const dbg = fakeDebugger();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    await attachCdp(dbg, target, 'cap-1', fakeClock(), onEvent);
    dbg.emit(target, { method: 'Console.messageAdded', params: { message: { level: 'weird', text: 'x' } } });
    expect((onEvent.mock.calls[0]![0].payload as ConsolePayload).level).toBe('log');
  });

  it('maps Runtime.exceptionThrown to an error console event', async () => {
    const dbg = fakeDebugger();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    await attachCdp(dbg, target, 'cap-1', fakeClock(), onEvent);
    dbg.emit(target, {
      method: 'Runtime.exceptionThrown',
      params: { exceptionDetails: { text: 'fallback text', exception: { description: 'boom' }, stackTrace: {} } },
    });
    expect(onEvent.mock.calls[0]![0].payload).toMatchObject({ level: 'error', text: 'boom' });
  });

  it('falls back to exceptionDetails.text when no exception description', async () => {
    const dbg = fakeDebugger();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    await attachCdp(dbg, target, 'cap-1', fakeClock(), onEvent);
    dbg.emit(target, { method: 'Runtime.exceptionThrown', params: { exceptionDetails: { text: 'fallback text' } } });
    expect(onEvent.mock.calls[0]![0].payload).toMatchObject({ level: 'error', text: 'fallback text', stack: null });
  });

  it('finishes a request with no prior responseReceived using defaults', async () => {
    const dbg = fakeDebugger();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    await attachCdp(dbg, target, 'cap-1', fakeClock(), onEvent);
    dbg.emit(target, {
      method: 'Network.requestWillBeSent',
      params: { requestId: 'r2', request: { url: 'https://x.test/b', method: 'GET' } },
    });
    dbg.emit(target, { method: 'Network.loadingFinished', params: { requestId: 'r2' } });
    expect(onEvent.mock.calls[0]![0].payload).toMatchObject({
      status: null,
      requestHeaders: {},
      responseHeaders: {},
      requestBody: null,
      sizeBytes: null,
    });
  });

  it('ignores unknown CDP methods', () => {
    const result = mapCdpEvent({ method: 'Unknown.thing', params: {} }, 'cap-1', fakeClock(), new Map());
    expect(result).toBeNull();
  });

  it('detach() removes the listener and detaches the debugger', async () => {
    const dbg = fakeDebugger();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    const session = await attachCdp(dbg, target, 'cap-1', fakeClock(), onEvent);
    await session.detach();
    expect(dbg.detach).toHaveBeenCalledWith(target);
    dbg.emit(target, { method: 'Console.messageAdded', params: { message: { level: 'log', text: 'after detach' } } });
    expect(onEvent).not.toHaveBeenCalled();
  });
});
