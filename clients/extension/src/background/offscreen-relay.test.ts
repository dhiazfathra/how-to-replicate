import { describe, expect, it, vi } from 'vitest';
import { createInstantReplay, createRedactor, createRingBuffer, parseRuleset } from '@htr/capture-core';
import { createOffscreenRelayListener } from './offscreen-relay.js';

const CAPTURE_ID = 'cap-1';

function fixedClock(t = 0) {
  return { epoch: 0, now: () => t };
}

function buffer(clock = fixedClock()) {
  const redactor = createRedactor(parseRuleset({ version: '1', rules: [] }));
  return createInstantReplay({ redactor, ring: createRingBuffer(), clock });
}

describe('createOffscreenRelayListener', () => {
  it('routes htr:video-chunk into the buffer', () => {
    const replay = buffer();
    const listener = createOffscreenRelayListener(CAPTURE_ID, replay, fixedClock(), vi.fn());
    const data = new Uint8Array([1, 2, 3]);

    listener({ type: 'htr:video-chunk', captureId: CAPTURE_ID, data });

    expect(replay.videoChunks()).toEqual([{ data, t: 0 }]);
  });

  it('routes htr:screenshot into the buffer', () => {
    const replay = buffer();
    const listener = createOffscreenRelayListener(CAPTURE_ID, replay, fixedClock(), vi.fn());

    listener({ type: 'htr:screenshot', captureId: CAPTURE_ID, dataUrl: 'data:image/png;base64,abc' });

    expect(replay.screenshots()).toEqual([{ dataUrl: 'data:image/png;base64,abc', t: 0 }]);
  });

  it('routes htr:lifecycle into the buffer as a redacted lifecycle event', () => {
    const replay = buffer();
    const listener = createOffscreenRelayListener(CAPTURE_ID, replay, fixedClock(5), vi.fn());

    listener({ type: 'htr:lifecycle', captureId: CAPTURE_ID, transition: 'frame-budget->degraded', detail: 'x' });

    const events = replay.events();
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({
      captureId: CAPTURE_ID,
      t: 5,
      kind: 'lifecycle',
      payload: { transition: 'frame-budget->degraded', detail: 'x' },
    });
  });

  it('routes htr:degraded to the markDegraded callback', () => {
    const replay = buffer();
    const markDegraded = vi.fn();
    const listener = createOffscreenRelayListener(CAPTURE_ID, replay, fixedClock(), markDegraded);

    listener({ type: 'htr:degraded', captureId: CAPTURE_ID });

    expect(markDegraded).toHaveBeenCalledOnce();
  });

  it('ignores a message for a different captureId', () => {
    const replay = buffer();
    const markDegraded = vi.fn();
    const listener = createOffscreenRelayListener(CAPTURE_ID, replay, fixedClock(), markDegraded);

    listener({ type: 'htr:degraded', captureId: 'cap-other' });
    listener({ type: 'htr:video-chunk', captureId: 'cap-other', data: new Uint8Array() });

    expect(markDegraded).not.toHaveBeenCalled();
    expect(replay.videoChunks()).toHaveLength(0);
  });

  it('ignores malformed messages', () => {
    const replay = buffer();
    const markDegraded = vi.fn();
    const listener = createOffscreenRelayListener(CAPTURE_ID, replay, fixedClock(), markDegraded);

    listener(null);
    listener('a string');
    listener({ captureId: CAPTURE_ID });
    listener({ type: 'unrelated:message', captureId: CAPTURE_ID });

    expect(markDegraded).not.toHaveBeenCalled();
    expect(replay.videoChunks()).toHaveLength(0);
  });
});
