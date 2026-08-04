import { describe, expect, it, vi } from 'vitest';
import type { CaptureEvent, Clock } from '@htr/capture-core';
import { buildDegradedHandoffEvent, startFallback } from './fallback.js';
import type { ChromeAction, ChromeWebRequest, WebRequestDetails } from '../lib/chrome-adapter.js';

function fakeClock(t = 0): Clock {
  return { epoch: 0, now: () => t };
}

function fakeWebRequest(): ChromeWebRequest & {
  fireBeforeRequest(d: WebRequestDetails): void;
  fireCompleted(d: WebRequestDetails): void;
} {
  const before: ((d: WebRequestDetails) => void)[] = [];
  const completed: ((d: WebRequestDetails) => void)[] = [];
  return {
    onBeforeRequest: {
      addListener: (l) => before.push(l),
      removeListener: (l) => {
        const i = before.indexOf(l);
        if (i >= 0) before.splice(i, 1);
      },
    },
    onCompleted: {
      addListener: (l) => completed.push(l),
      removeListener: (l) => {
        const i = completed.indexOf(l);
        if (i >= 0) completed.splice(i, 1);
      },
    },
    fireBeforeRequest: (d) => before.forEach((l) => l(d)),
    fireCompleted: (d) => completed.forEach((l) => l(d)),
  };
}

function fakeAction(): ChromeAction & { calls: { text: string; tabId?: number }[] } {
  const calls: { text: string; tabId?: number }[] = [];
  return {
    calls,
    setBadgeText: (details) => {
      calls.push(details);
      return Promise.resolve();
    },
    onClicked: { addListener: () => undefined, removeListener: () => undefined },
  };
}

describe('startFallback', () => {
  const target = { tabId: 7 };

  it('sets the degraded badge on the target tab', () => {
    const action = fakeAction();
    startFallback(fakeWebRequest(), action, target, 'cap-1', fakeClock(), () => undefined);
    expect(action.calls).toEqual([{ text: 'DEG', tabId: 7 }]);
  });

  it('maps a completed request into a network CaptureEvent with bodyDropped', () => {
    const wr = fakeWebRequest();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    startFallback(wr, fakeAction(), target, 'cap-1', fakeClock(3), onEvent);

    wr.fireBeforeRequest({ requestId: 'r1', url: 'https://x.test', method: 'GET', tabId: 7 });
    wr.fireCompleted({
      requestId: 'r1',
      url: 'https://x.test',
      method: 'GET',
      tabId: 7,
      statusCode: 204,
      responseHeaders: [{ name: 'A', value: '1' }, { name: 'B' }],
    });

    expect(onEvent).toHaveBeenCalledTimes(1);
    const event = onEvent.mock.calls[0]![0];
    expect(event.t).toBe(3);
    expect(event.payload).toMatchObject({
      method: 'GET',
      url: 'https://x.test',
      status: 204,
      bodyDropped: true,
      responseHeaders: { A: '1' },
    });
  });

  it('ignores requests for other tabs', () => {
    const wr = fakeWebRequest();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    startFallback(wr, fakeAction(), target, 'cap-1', fakeClock(), onEvent);
    wr.fireBeforeRequest({ requestId: 'r1', url: 'x', method: 'GET', tabId: 999 });
    wr.fireCompleted({ requestId: 'r1', url: 'x', method: 'GET', tabId: 999, statusCode: 200 });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it('ignores a completed event for a request it never saw begin', () => {
    const wr = fakeWebRequest();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    startFallback(wr, fakeAction(), target, 'cap-1', fakeClock(), onEvent);
    wr.fireCompleted({ requestId: 'ghost', url: 'x', method: 'GET', tabId: 7, statusCode: 200 });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it('defaults status to null and responseHeaders to {} when absent', () => {
    const wr = fakeWebRequest();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    startFallback(wr, fakeAction(), target, 'cap-1', fakeClock(), onEvent);
    wr.fireBeforeRequest({ requestId: 'r2', url: 'https://x.test', method: 'POST', tabId: 7 });
    wr.fireCompleted({ requestId: 'r2', url: 'https://x.test', method: 'POST', tabId: 7 });
    expect(onEvent.mock.calls[0]![0].payload).toMatchObject({ status: null, responseHeaders: {} });
  });

  it('stop() removes both listeners', () => {
    const wr = fakeWebRequest();
    const onEvent = vi.fn<(event: CaptureEvent) => void>();
    const session = startFallback(wr, fakeAction(), target, 'cap-1', fakeClock(), onEvent);
    session.stop();
    wr.fireBeforeRequest({ requestId: 'r1', url: 'x', method: 'GET', tabId: 7 });
    wr.fireCompleted({ requestId: 'r1', url: 'x', method: 'GET', tabId: 7, statusCode: 200 });
    expect(onEvent).not.toHaveBeenCalled();
  });
});

describe('buildDegradedHandoffEvent', () => {
  it('builds a lifecycle event recording the handoff reason', () => {
    const event = buildDegradedHandoffEvent('cap-1', fakeClock(9), 'target_closed');
    expect(event).toMatchObject({
      captureId: 'cap-1',
      t: 9,
      kind: 'lifecycle',
      payload: { transition: 'cdp-detach->fallback', detail: 'target_closed' },
      redaction: { rulesApplied: [], fidelity: 'full' },
    });
  });
});
