import { describe, expect, it, vi } from 'vitest';
import { createInstantReplay, createRedactor, createRingBuffer, parseRuleset } from '@htr/capture-core';
import { encodeChunk } from '../lib/chunk-codec.js';
import { createOffscreenRelayListener, createScreenshotRequestListener } from './offscreen-relay.js';

const CAPTURE_ID = 'cap-1';

function fixedClock(t = 0) {
  return { epoch: 0, now: () => t };
}

function buffer(clock = fixedClock()) {
  const redactor = createRedactor(parseRuleset({ version: '1', rules: [] }));
  return createInstantReplay({ redactor, ring: createRingBuffer(), clock });
}

describe('createOffscreenRelayListener', () => {
  it('routes htr:video-chunk into the buffer, decoding the base64 wire payload', async () => {
    const replay = buffer();
    const listener = createOffscreenRelayListener(CAPTURE_ID, replay, fixedClock(), vi.fn());
    const original = new Uint8Array([1, 2, 3]);
    const encoded = await encodeChunk(original);

    // The regression this guards: `chrome.runtime.sendMessage` JSON-serializes
    // its payload, so a raw Uint8Array/Blob never survives the trip — the
    // wire value must already be a plain JSON-safe string by the time it
    // reaches this listener.
    expect(typeof encoded).toBe('string');
    listener({ type: 'htr:video-chunk', captureId: CAPTURE_ID, data: encoded });

    expect(replay.videoChunks()).toEqual([{ data: original, t: 0 }]);
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

describe('createScreenshotRequestListener', () => {
  it('captures the visible tab and returns the result via sendResponse, keeping the channel open', async () => {
    const captureVisibleTab = vi.fn().mockResolvedValue('data:image/png;base64,abc');
    const listener = createScreenshotRequestListener(CAPTURE_ID, { captureVisibleTab });
    const sendResponse = vi.fn();

    const keepOpen = listener({ type: 'htr:capture-screenshot-request', captureId: CAPTURE_ID }, undefined, sendResponse);

    expect(keepOpen).toBe(true);
    await vi.waitFor(() => expect(sendResponse).toHaveBeenCalledWith('data:image/png;base64,abc'));
  });

  it('ignores a request for a different captureId', () => {
    const captureVisibleTab = vi.fn().mockResolvedValue('x');
    const listener = createScreenshotRequestListener(CAPTURE_ID, { captureVisibleTab });
    const sendResponse = vi.fn();

    const result = listener({ type: 'htr:capture-screenshot-request', captureId: 'cap-other' }, undefined, sendResponse);

    expect(result).toBeUndefined();
    expect(captureVisibleTab).not.toHaveBeenCalled();
  });

  it('ignores malformed messages', () => {
    const captureVisibleTab = vi.fn().mockResolvedValue('x');
    const listener = createScreenshotRequestListener(CAPTURE_ID, { captureVisibleTab });
    const sendResponse = vi.fn();

    expect(listener(null, undefined, sendResponse)).toBeUndefined();
    expect(listener({ type: 'htr:degraded', captureId: CAPTURE_ID }, undefined, sendResponse)).toBeUndefined();
    expect(captureVisibleTab).not.toHaveBeenCalled();
  });
});
