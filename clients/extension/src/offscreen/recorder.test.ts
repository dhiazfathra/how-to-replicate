import { describe, expect, it, vi } from 'vitest';
import type { DrawTarget } from './blur.js';
import { startRecorder, type CanvasTarget, type DegradeSink, type MediaRecorderFactory, type Scheduler } from './recorder.js';
import type { IntervalTimer, ScreenshotCapture } from './screenshot-fallback.js';
import type { MediaRecorderLike } from './timeslice.js';

function fakeCtx(): DrawTarget {
  return {
    filter: 'none',
    drawImage: (): void => undefined,
    save: (): void => undefined,
    restore: (): void => undefined,
    beginPath: (): void => undefined,
    rect: (): void => undefined,
    clip: (): void => undefined,
  };
}

function fakeCanvas(stream: unknown = 'canvas-stream'): CanvasTarget {
  return {
    getContext: () => fakeCtx(),
    captureStream: () => stream,
  };
}

function fakeScheduler(): Scheduler & { step(): void; pending(): boolean; currentCallback(): (() => void) | null } {
  let cb: (() => void) | null = null;
  return {
    requestFrame(callback: () => void): void {
      cb = callback;
    },
    step(): void {
      const current = cb;
      cb = null;
      current?.();
    },
    pending(): boolean {
      return cb !== null;
    },
    currentCallback(): (() => void) | null {
      return cb;
    },
  };
}

function fakeRecorder(): MediaRecorderLike {
  return {
    state: 'recording',
    ondataavailable: null,
    start: (): void => undefined,
    stop: (): void => undefined,
  };
}

function fakeClock(times: number[]): { now(): number } {
  let i = 0;
  return { now: () => times[Math.min(i++, times.length - 1)]! };
}

function fakeSink(): DegradeSink & {
  events: { transition: string; detail: string | null }[];
  degraded: boolean;
  screenshots: string[];
} {
  const events: { transition: string; detail: string | null }[] = [];
  const screenshots: string[] = [];
  return {
    events,
    degraded: false,
    screenshots,
    pushVideoChunk: (): void => undefined,
    pushScreenshot(dataUrl): void {
      screenshots.push(dataUrl);
    },
    appendLifecycleEvent(transition, detail): void {
      events.push({ transition, detail });
    },
    markDegraded(): void {
      this.degraded = true;
    },
  };
}

function fakeScreenshot(dataUrl = 'data:shot'): ScreenshotCapture & { calls: number } {
  return {
    calls: 0,
    captureVisibleTab(): Promise<string> {
      this.calls += 1;
      return Promise.resolve(dataUrl);
    },
  };
}

function fakeTimer(): IntervalTimer & { intervals: number[]; cleared: unknown[] } {
  const intervals: number[] = [];
  const cleared: unknown[] = [];
  let nextHandle = 0;
  return {
    intervals,
    cleared,
    setInterval(_callback, ms): unknown {
      intervals.push(ms);
      nextHandle += 1;
      return nextHandle;
    },
    clearInterval(handle): void {
      cleared.push(handle);
    },
  };
}

const source = { width: 100, height: 50, frame: 'video-frame' };

function start(
  overrides: Partial<{
    canvas: CanvasTarget;
    createMediaRecorder: MediaRecorderFactory;
    scheduler: ReturnType<typeof fakeScheduler>;
    clock: { now(): number };
    sink: ReturnType<typeof fakeSink>;
    screenshot: ReturnType<typeof fakeScreenshot>;
    timer: ReturnType<typeof fakeTimer>;
  }> = {},
) {
  const scheduler = overrides.scheduler ?? fakeScheduler();
  const sink = overrides.sink ?? fakeSink();
  const screenshot = overrides.screenshot ?? fakeScreenshot();
  const timer = overrides.timer ?? fakeTimer();
  const handle = startRecorder(
    source,
    overrides.canvas ?? fakeCanvas(),
    overrides.createMediaRecorder ?? ((): MediaRecorderLike => fakeRecorder()),
    scheduler,
    overrides.clock ?? fakeClock([0, 1]),
    sink,
    screenshot,
    timer,
  );
  return { handle, scheduler, sink, screenshot, timer };
}

describe('startRecorder', () => {
  it('constructs MediaRecorder with the canvas stream, never the display frame', () => {
    const createMediaRecorder: MediaRecorderFactory = vi.fn(() => fakeRecorder());
    start({ canvas: fakeCanvas('canvas-stream'), createMediaRecorder, clock: fakeClock([0, 0]) });

    expect(createMediaRecorder).toHaveBeenCalledWith('canvas-stream');
    expect(createMediaRecorder).not.toHaveBeenCalledWith(source.frame);
  });

  it('keeps compositing and re-scheduling frames under budget', () => {
    const { scheduler, handle, sink } = start();

    scheduler.step();
    expect(scheduler.pending()).toBe(true);
    expect(handle.isDegraded()).toBe(false);
    expect(sink.degraded).toBe(false);
  });

  it('stops recording, degrades the handle, and starts periodic screenshots when regions fail to resolve', async () => {
    const recorder = fakeRecorder();
    const stopSpy = vi.spyOn(recorder, 'stop');
    const { handle, scheduler, sink, screenshot, timer } = start({
      createMediaRecorder: () => recorder,
      clock: fakeClock([0, 0]),
    });

    handle.updateRegions([{ x: NaN, y: 0, width: 1, height: 1 }]);
    scheduler.step();

    expect(stopSpy).toHaveBeenCalledOnce();
    expect(sink.degraded).toBe(true);
    expect(sink.events).toEqual([{ transition: 'video->screenshot-only', detail: 'blur-region-resolution-failed' }]);
    expect(scheduler.pending()).toBe(false);

    // Regression: the handle itself must report degraded, not just the sink
    // (a caller gating on `handle.isDegraded()` must not misread this as healthy).
    expect(handle.isDegraded()).toBe(true);

    // Periodic-screenshot floor started: one immediate capture, plus an interval.
    expect(screenshot.calls).toBe(1);
    await Promise.resolve();
    expect(sink.screenshots).toEqual(['data:shot']);
    expect(timer.intervals).toHaveLength(1);

    // Never re-enables: further steps are no-ops.
    scheduler.step();
    expect(scheduler.pending()).toBe(false);
  });

  it('stops recording and degrades exactly once when the frame budget is exceeded, starting screenshots too', () => {
    const recorder = fakeRecorder();
    const stopSpy = vi.spyOn(recorder, 'stop');
    // 4 consecutive over-budget frames trips the monitor on the 4th tick.
    const times = [0, 20, 20, 40, 40, 60, 60, 80, 80, 100];
    const { handle, scheduler, sink, screenshot } = start({ createMediaRecorder: () => recorder, clock: fakeClock(times) });

    scheduler.step();
    scheduler.step();
    scheduler.step();
    scheduler.step();

    expect(stopSpy).toHaveBeenCalledOnce();
    expect(sink.degraded).toBe(true);
    expect(sink.events).toEqual([{ transition: 'video->screenshot-only', detail: 'frame-budget-exceeded' }]);
    expect(handle.isDegraded()).toBe(true);
    expect(scheduler.pending()).toBe(false);
    expect(screenshot.calls).toBe(1);
  });

  it('ignores a frame tick that fires after stop() (defensive against an in-flight rAF)', () => {
    const recorder = fakeRecorder();
    const stopSpy = vi.spyOn(recorder, 'stop');
    const { handle, scheduler } = start({ createMediaRecorder: () => recorder, clock: fakeClock([0, 0]) });

    const inFlightTick = scheduler.currentCallback();
    handle.stop();
    inFlightTick?.();

    expect(stopSpy).toHaveBeenCalledOnce();
  });

  it('stop() halts scheduling idempotently without touching an already-inactive recorder twice', () => {
    const recorder = fakeRecorder();
    const stopSpy = vi.spyOn(recorder, 'stop');
    const { handle } = start({ createMediaRecorder: () => recorder, clock: fakeClock([0, 0]) });

    handle.stop();
    handle.stop();

    expect(stopSpy).toHaveBeenCalledOnce();
  });

  it('stop() after a degrade also tears down the periodic-screenshot interval', () => {
    const recorder = fakeRecorder();
    const { handle, scheduler, timer } = start({ createMediaRecorder: () => recorder, clock: fakeClock([0, 0]) });

    handle.updateRegions([{ x: NaN, y: 0, width: 1, height: 1 }]);
    scheduler.step();
    handle.stop();

    expect(timer.cleared).toEqual([1]);
  });

  it('updateRegions replaces the region list used by the next composite', () => {
    const { handle, scheduler } = start();

    handle.updateRegions([{ x: 0, y: 0, width: 5, height: 5 }]);
    scheduler.step();

    expect(scheduler.pending()).toBe(true);
  });
});
