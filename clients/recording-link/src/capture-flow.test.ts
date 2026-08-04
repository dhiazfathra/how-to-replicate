import { describe, expect, it, vi } from 'vitest';
import type { Clock, DrawTarget, EnvSnapshot } from '@htr/capture-core';
import {
  finalizeRecording,
  RecordingFinalizeError,
  startRecording,
  type CanvasTarget,
  type FrameSource,
  type MediaRecorderLike,
  type RecorderChunk,
  type Scheduler,
} from './capture-flow.js';
import { BUNDLED_RULESET } from './ruleset.js';

function fakeClock(startEpoch = 1_000): Clock {
  let t = 0;
  return {
    epoch: startEpoch,
    now: (): number => {
      t += 1;
      return t;
    },
  };
}

const ENV: EnvSnapshot = {
  userAgent: 'test-agent',
  platform: 'test',
  viewport: { w: 100, h: 100 },
  devicePixelRatio: 1,
  locale: 'en-US',
  timezone: 'UTC',
  url: 'https://example.test/',
};

function fakeDrawTarget(): DrawTarget & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    filter: 'none',
    drawImage: (): void => void calls.push('drawImage'),
    save: (): void => void calls.push('save'),
    restore: (): void => void calls.push('restore'),
    beginPath: (): void => void calls.push('beginPath'),
    rect: (): void => void calls.push('rect'),
    clip: (): void => void calls.push('clip'),
  };
}

describe('startRecording', () => {
  it('draws frames through compositeFrame and forwards recorder chunks', () => {
    const ctx = fakeDrawTarget();
    const source: FrameSource = { width: 10, height: 10, frame: 'video' };
    const canvas: CanvasTarget = { getContext: () => ctx, captureStream: () => 'stream' };

    const start = vi.fn();
    const stopFn = vi.fn();
    let recorder!: MediaRecorderLike;
    const createMediaRecorder = vi.fn((canvasStream: unknown) => {
      expect(canvasStream).toBe('stream');
      recorder = {
        state: 'inactive',
        start,
        stop: stopFn,
        ondataavailable: null,
      };
      return recorder;
    });

    let tick: (() => void) | undefined;
    const scheduler: Scheduler = {
      requestFrame: (cb) => {
        tick = cb;
      },
    };

    const chunks: RecorderChunk[] = [];
    const handle = startRecording({
      source,
      canvas,
      createMediaRecorder,
      scheduler,
      onChunk: (chunk) => chunks.push(chunk),
    });

    expect(start).toHaveBeenCalledWith(1000);
    expect(ctx.calls).toEqual([]);

    // The fake scheduler only captures the callback; run it to draw a frame.
    tick?.();
    expect(ctx.calls).toEqual(['drawImage']);

    // A second scheduled tick keeps drawing while not stopped.
    tick?.();
    expect(ctx.calls).toEqual(['drawImage', 'drawImage']);

    const data = new Uint8Array([1, 2, 3]);
    recorder.ondataavailable?.({ data });
    expect(chunks).toEqual([data]);

    // A falsy chunk is not forwarded.
    recorder.ondataavailable?.({ data: undefined as unknown as RecorderChunk });
    expect(chunks).toEqual([data]);

    handle.stop();
    expect(stopFn).toHaveBeenCalledOnce();

    // Stopping twice is a no-op; a tick after stop draws nothing further.
    handle.stop();
    expect(stopFn).toHaveBeenCalledOnce();
    const callsBeforeTick = ctx.calls.length;
    tick?.();
    expect(ctx.calls).toHaveLength(callsBeforeTick);
  });
});

describe('finalizeRecording', () => {
  it('walks recording -> redacting -> composing -> ready and builds a bundle', async () => {
    const ids = ['capture-1', 'asset-1'];
    let idIndex = 0;

    const result = await finalizeRecording({
      ruleset: BUNDLED_RULESET,
      chunks: [
        new Uint8Array([1, 2, 3]),
        { arrayBuffer: () => Promise.resolve(new Uint8Array([4, 5]).buffer) } as unknown as Blob,
      ],
      env: ENV,
      clock: fakeClock(),
      createdAt: '2026-08-04T00:00:00.000Z',
      idFactory: () => ids[idIndex++] as string,
    });

    expect(result.capture.state).toBe('ready');
    expect(result.capture.id).toBe('capture-1');
    expect(result.capture.fidelity).toBe('full');
    expect(result.capture.withheldEventCount).toBe(0);
    expect(result.capture.metadata.rulesetVersion).toBe(BUNDLED_RULESET.version);
    expect(result.capture.assets).toHaveLength(1);
    expect(result.capture.assets[0]?.sizeBytes).toBe(5);
    expect(result.capture.assets[0]?.chunkCount).toBe(2);
    expect(result.capture.assets[0]?.sha256).toMatch(/^[0-9a-f]{64}$/);
    expect(result.bundle.byteLength).toBeGreaterThan(0);
  });

  it('marks fidelity degraded when the redactor withholds events', async () => {
    // `parseRuleset` validates a pattern rule's regex at parse time, so
    // build the ruleset object directly (bypassing `parseRuleset`) to get a
    // rule that instead fails at *apply* time — the engine drops any event
    // a rule throws on, which is the withheld-event path this exercises.
    const badRuleset = {
      version: 'v-throws',
      rules: [{ id: 'p1', class: 'pattern' as const, pattern: '(', label: 'broken' }],
    };

    const result = await finalizeRecording({
      ruleset: badRuleset,
      chunks: [],
      env: ENV,
      clock: fakeClock(),
    });

    expect(result.capture.fidelity).toBe('degraded');
    expect(result.capture.withheldEventCount).toBeGreaterThan(0);
  });

  it('throws RecordingFinalizeError with a failed capture when finalizing fails', async () => {
    const failingChunk = {
      arrayBuffer: () => Promise.reject(new Error('boom')),
    } as unknown as Blob;

    await expect(
      finalizeRecording({
        ruleset: BUNDLED_RULESET,
        chunks: [failingChunk],
        env: ENV,
        clock: fakeClock(),
      }),
    ).rejects.toSatisfy((error: unknown) => {
      expect(error).toBeInstanceOf(RecordingFinalizeError);
      const finalizeError = error as RecordingFinalizeError;
      expect(finalizeError.message).toBe('boom');
      expect(finalizeError.capture.state).toBe('failed');
      return true;
    });
  });

  it('stringifies a non-Error throw', async () => {
    const failingChunk = {
      // eslint-disable-next-line @typescript-eslint/prefer-promise-reject-errors -- exercising the non-Error branch of errorMessage()
      arrayBuffer: () => Promise.reject('boom-string'),
    } as unknown as Blob;

    await expect(
      finalizeRecording({
        ruleset: BUNDLED_RULESET,
        chunks: [failingChunk],
        env: ENV,
        clock: fakeClock(),
      }),
    ).rejects.toMatchObject({ message: 'boom-string' });
  });

  it('defaults idFactory and createdAt when not provided', async () => {
    const result = await finalizeRecording({
      ruleset: BUNDLED_RULESET,
      chunks: [],
      env: ENV,
      clock: fakeClock(),
    });

    expect(result.capture.id).toEqual(expect.any(String));
    expect(result.capture.createdAt).toEqual(expect.any(String));
  });

  it('never calls fetch — export-only, no egress but the download', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('fetch must not be called'));

    await finalizeRecording({
      ruleset: BUNDLED_RULESET,
      chunks: [new Uint8Array([1, 2, 3])],
      env: ENV,
      clock: fakeClock(),
    });

    expect(fetchSpy).not.toHaveBeenCalled();
    fetchSpy.mockRestore();
  });
});
