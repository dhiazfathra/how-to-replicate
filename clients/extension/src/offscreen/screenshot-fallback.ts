/** Interval between degraded-mode screenshots. */
export const SCREENSHOT_INTERVAL_MS = 5000;

export type ScreenshotCapture = {
  captureVisibleTab(): Promise<string>;
};

/** Structural subset of the `setInterval`/`clearInterval` pair — injected so tests never start a real timer. */
export type IntervalTimer = {
  setInterval(callback: () => void, ms: number): unknown;
  clearInterval(handle: unknown): void;
};

export type ScreenshotSink = {
  pushScreenshot(dataUrl: string): void;
};

export type ScreenshotFallback = {
  stop(): void;
};

/**
 * The fail-closed floor once video stops: keep taking screenshots so a
 * degraded capture still carries visual evidence (brief: "keep periodic
 * screenshots"). Fires once immediately, then on `intervalMs` — a capture
 * that degrades between two screenshots shouldn't wait a full interval for
 * its first one.
 */
export function startPeriodicScreenshots(
  capture: ScreenshotCapture,
  timer: IntervalTimer,
  sink: ScreenshotSink,
  intervalMs: number = SCREENSHOT_INTERVAL_MS,
): ScreenshotFallback {
  const tick = (): void => {
    void capture.captureVisibleTab().then((dataUrl) => sink.pushScreenshot(dataUrl));
  };

  tick();
  const handle = timer.setInterval(tick, intervalMs);

  return {
    stop(): void {
      timer.clearInterval(handle);
    },
  };
}
