import type { RectLike } from '../lib/dom-types.js';
import { compositeFrame, validateRegions, type DrawTarget } from './blur.js';
import { createFrameBudgetMonitor } from './frame-budget.js';
import {
  startPeriodicScreenshots,
  type IntervalTimer,
  type ScreenshotCapture,
  type ScreenshotFallback,
  type ScreenshotSink,
} from './screenshot-fallback.js';
import { startTimesliceRecording, stopTimesliceRecording, type ChunkSink, type MediaRecorderLike } from './timeslice.js';

/** Structural subset of `OffscreenCanvas` this module draws to and records from. */
export type CanvasTarget = {
  getContext(kind: '2d'): DrawTarget;
  captureStream(): unknown;
};

/** The frame source is read fresh on every tick — a `<video>` element or `ImageBitmap` the caller keeps current. */
export type FrameSource = {
  width: number;
  height: number;
  frame: unknown;
};

export type Scheduler = {
  requestFrame(callback: () => void): void;
};

export type Clock = {
  now(): number;
};

/** Never called with anything but the canvas stream — the display stream never reaches this factory. */
export type MediaRecorderFactory = (canvasStream: unknown) => MediaRecorderLike;

export type DegradeSink = ChunkSink &
  ScreenshotSink & {
    /** Record the video->screenshot-only handoff as a `lifecycle` event. */
    appendLifecycleEvent(transition: string, detail: string | null): void;
    /** Flip the capture to `fidelity: 'degraded'` (invariant 4). */
    markDegraded(): void;
  };

export type RecorderHandle = {
  /** Replace the current blur regions (fed by Task 11's per-frame `BlurRegionsMessage`). */
  updateRegions(regions: readonly RectLike[]): void;
  stop(): void;
  isDegraded(): boolean;
};

/**
 * Drive the offscreen recorder: draw + blur each frame onto `canvas`, feed
 * **the canvas's own `MediaStream`** to `MediaRecorder` in timeslice mode,
 * and monitor the compositing budget. The display stream backing
 * `source.frame` is only ever read as a `drawImage` source — it is never
 * passed to `createMediaRecorder`, so it can never be persisted unblurred
 * (invariant 2).
 *
 * Two failure paths converge on the same degrade-and-stop behavior, and
 * neither one re-enables video afterward:
 *  - a blur-region list that fails validation (resolution failure), and
 *  - the frame-budget monitor tripping.
 *
 * Either path also starts the periodic-screenshot floor (`screenshot` +
 * `timer`) so a degraded capture still carries visual evidence instead of
 * going dark — the brief's "keep periodic screenshots" requirement.
 */
export function startRecorder(
  source: FrameSource,
  canvas: CanvasTarget,
  createMediaRecorder: MediaRecorderFactory,
  scheduler: Scheduler,
  clock: Clock,
  sink: DegradeSink,
  screenshot: ScreenshotCapture,
  timer: IntervalTimer,
): RecorderHandle {
  const ctx = canvas.getContext('2d');
  const budget = createFrameBudgetMonitor();
  const recorder = createMediaRecorder(canvas.captureStream());
  startTimesliceRecording(recorder, sink);

  let regions: readonly RectLike[] = [];
  let stopped = false;
  let degraded = false;
  let screenshotFallback: ScreenshotFallback | null = null;

  function stopForDegrade(detail: string): void {
    stopped = true;
    degraded = true;
    stopTimesliceRecording(recorder);
    sink.markDegraded();
    sink.appendLifecycleEvent('video->screenshot-only', detail);
    screenshotFallback = startPeriodicScreenshots(screenshot, timer, sink);
  }

  const tick = (): void => {
    if (stopped) return;

    const resolved = validateRegions(regions);
    if (resolved === null) {
      stopForDegrade('blur-region-resolution-failed');
      return;
    }

    const start = clock.now();
    compositeFrame(ctx, source.frame, source.width, source.height, resolved);
    const durationMs = clock.now() - start;

    if (budget.record(durationMs)) {
      stopForDegrade('frame-budget-exceeded');
      return;
    }

    scheduler.requestFrame(tick);
  };

  scheduler.requestFrame(tick);

  return {
    updateRegions(next: readonly RectLike[]): void {
      regions = next;
    },
    stop(): void {
      screenshotFallback?.stop();
      screenshotFallback = null;
      if (stopped) return;
      stopped = true;
      stopTimesliceRecording(recorder);
    },
    isDegraded(): boolean {
      return degraded || budget.isDegraded();
    },
  };
}
