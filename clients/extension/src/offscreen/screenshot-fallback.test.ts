import { describe, expect, it, vi } from 'vitest';
import { SCREENSHOT_INTERVAL_MS, startPeriodicScreenshots, type IntervalTimer, type ScreenshotCapture } from './screenshot-fallback.js';

function fakeTimer(): IntervalTimer & { intervals: number[]; cleared: unknown[]; fire(handle: unknown): void } {
  const intervals: number[] = [];
  const cleared: unknown[] = [];
  const callbacks = new Map<unknown, () => void>();
  let nextHandle = 0;
  return {
    intervals,
    cleared,
    setInterval(callback, ms): unknown {
      intervals.push(ms);
      nextHandle += 1;
      callbacks.set(nextHandle, callback);
      return nextHandle;
    },
    clearInterval(handle): void {
      cleared.push(handle);
    },
    fire(handle): void {
      callbacks.get(handle)?.();
    },
  };
}

function fakeCapture(dataUrl = 'data:one'): ScreenshotCapture & { calls: number } {
  return {
    calls: 0,
    captureVisibleTab(): Promise<string> {
      this.calls += 1;
      return Promise.resolve(dataUrl);
    },
  };
}

describe('startPeriodicScreenshots', () => {
  it('captures immediately and pushes the result to the sink', async () => {
    const capture = fakeCapture('data:first');
    const timer = fakeTimer();
    const sink = { pushScreenshot: vi.fn() };

    startPeriodicScreenshots(capture, timer, sink);
    expect(capture.calls).toBe(1);
    await Promise.resolve();

    expect(sink.pushScreenshot).toHaveBeenCalledWith('data:first');
  });

  it('schedules the interval at the default interval and again on every tick', async () => {
    const capture = fakeCapture();
    const timer = fakeTimer();
    const sink = { pushScreenshot: vi.fn() };

    startPeriodicScreenshots(capture, timer, sink);
    expect(timer.intervals).toEqual([SCREENSHOT_INTERVAL_MS]);

    timer.fire(1);
    await Promise.resolve();
    expect(capture.calls).toBe(2);
    expect(sink.pushScreenshot).toHaveBeenCalledTimes(2);
  });

  it('honors a custom interval', () => {
    const timer = fakeTimer();
    startPeriodicScreenshots(fakeCapture(), timer, { pushScreenshot: vi.fn() }, 2000);
    expect(timer.intervals).toEqual([2000]);
  });

  it('stop() clears the underlying interval', () => {
    const timer = fakeTimer();
    const fallback = startPeriodicScreenshots(fakeCapture(), timer, { pushScreenshot: vi.fn() });

    fallback.stop();

    expect(timer.cleared).toEqual([1]);
  });
});
