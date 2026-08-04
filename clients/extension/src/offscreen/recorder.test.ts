import { describe, expect, it, vi } from 'vitest';
import type { DrawTarget } from './blur.js';
import { startRecorder, type CanvasTarget, type DegradeSink, type MediaRecorderFactory, type Scheduler } from './recorder.js';
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

function fakeSink(): DegradeSink & { events: { transition: string; detail: string | null }[]; degraded: boolean } {
  const events: { transition: string; detail: string | null }[] = [];
  return {
    events,
    degraded: false,
    pushVideoChunk: (): void => undefined,
    appendLifecycleEvent(transition, detail): void {
      events.push({ transition, detail });
    },
    markDegraded(): void {
      this.degraded = true;
    },
  };
}

const source = { width: 100, height: 50, frame: 'video-frame' };

describe('startRecorder', () => {
  it('constructs MediaRecorder with the canvas stream, never the display frame', () => {
    const scheduler = fakeScheduler();
    const createMediaRecorder: MediaRecorderFactory = vi.fn(() => fakeRecorder());
    startRecorder(source, fakeCanvas('canvas-stream'), createMediaRecorder, scheduler, fakeClock([0, 0]), fakeSink());

    expect(createMediaRecorder).toHaveBeenCalledWith('canvas-stream');
    expect(createMediaRecorder).not.toHaveBeenCalledWith(source.frame);
  });

  it('keeps compositing and re-scheduling frames under budget', () => {
    const scheduler = fakeScheduler();
    const sink = fakeSink();
    const handle = startRecorder(source, fakeCanvas(), () => fakeRecorder(), scheduler, fakeClock([0, 1]), sink);

    scheduler.step();
    expect(scheduler.pending()).toBe(true);
    expect(handle.isDegraded()).toBe(false);
    expect(sink.degraded).toBe(false);
  });

  it('stops recording and degrades when regions fail to resolve, without compositing a frame', () => {
    const scheduler = fakeScheduler();
    const recorder = fakeRecorder();
    const stopSpy = vi.spyOn(recorder, 'stop');
    const sink = fakeSink();
    const handle = startRecorder(source, fakeCanvas(), () => recorder, scheduler, fakeClock([0, 0]), sink);

    handle.updateRegions([{ x: NaN, y: 0, width: 1, height: 1 }]);
    scheduler.step();

    expect(stopSpy).toHaveBeenCalledOnce();
    expect(sink.degraded).toBe(true);
    expect(sink.events).toEqual([{ transition: 'video->screenshot-only', detail: 'blur-region-resolution-failed' }]);
    expect(scheduler.pending()).toBe(false);

    // Never re-enables: further steps are no-ops.
    scheduler.step();
    expect(scheduler.pending()).toBe(false);
  });

  it('stops recording and degrades exactly once when the frame budget is exceeded', () => {
    const scheduler = fakeScheduler();
    const recorder = fakeRecorder();
    const stopSpy = vi.spyOn(recorder, 'stop');
    const sink = fakeSink();
    // 4 consecutive over-budget frames trips the monitor on the 4th tick.
    const times = [0, 20, 20, 40, 40, 60, 60, 80, 80, 100];
    const handle = startRecorder(source, fakeCanvas(), () => recorder, scheduler, fakeClock(times), sink);

    scheduler.step();
    scheduler.step();
    scheduler.step();
    scheduler.step();

    expect(stopSpy).toHaveBeenCalledOnce();
    expect(sink.degraded).toBe(true);
    expect(sink.events).toEqual([{ transition: 'video->screenshot-only', detail: 'frame-budget-exceeded' }]);
    expect(handle.isDegraded()).toBe(true);
    expect(scheduler.pending()).toBe(false);
  });

  it('ignores a frame tick that fires after stop() (defensive against an in-flight rAF)', () => {
    const scheduler = fakeScheduler();
    const recorder = fakeRecorder();
    const stopSpy = vi.spyOn(recorder, 'stop');
    const handle = startRecorder(source, fakeCanvas(), () => recorder, scheduler, fakeClock([0, 0]), fakeSink());

    const inFlightTick = scheduler.currentCallback();
    handle.stop();
    inFlightTick?.();

    expect(stopSpy).toHaveBeenCalledOnce();
  });

  it('stop() halts scheduling idempotently without touching an already-inactive recorder twice', () => {
    const scheduler = fakeScheduler();
    const recorder = fakeRecorder();
    const stopSpy = vi.spyOn(recorder, 'stop');
    const handle = startRecorder(source, fakeCanvas(), () => recorder, scheduler, fakeClock([0, 0]), fakeSink());

    handle.stop();
    handle.stop();

    expect(stopSpy).toHaveBeenCalledOnce();
  });

  it('updateRegions replaces the region list used by the next composite', () => {
    const scheduler = fakeScheduler();
    const handle = startRecorder(source, fakeCanvas(), () => fakeRecorder(), scheduler, fakeClock([0, 1]), fakeSink());

    handle.updateRegions([{ x: 0, y: 0, width: 5, height: 5 }]);
    scheduler.step();

    expect(scheduler.pending()).toBe(true);
  });
});
