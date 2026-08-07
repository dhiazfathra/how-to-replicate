/**
 * Per-frame compositing budget, one 60fps frame.
 */
export const FRAME_BUDGET_MS = 16.7;

/** More than this many consecutive frames over budget trips the degrade. */
export const CONSECUTIVE_OVER_THRESHOLD = 3;

/** Rolling-mean window size (frames) for the sustained-slowness check. */
export const ROLLING_WINDOW = 30;

export type FrameBudgetMonitor = {
  /**
   * Record one frame's compositing duration. Returns `true` exactly once —
   * the frame on which either threshold first trips — and `false` on every
   * other call, including all calls after degradation (there is no path
   * back to `false`, matching invariant 2's "never re-enables video").
   */
  record(durationMs: number): boolean;
  isDegraded(): boolean;
};

export function createFrameBudgetMonitor(): FrameBudgetMonitor {
  let consecutiveOver = 0;
  const window: number[] = [];
  let degraded = false;

  return {
    record(durationMs: number): boolean {
      if (degraded) return false;

      consecutiveOver = durationMs > FRAME_BUDGET_MS ? consecutiveOver + 1 : 0;

      window.push(durationMs);
      if (window.length > ROLLING_WINDOW) window.shift();

      const overConsecutive = consecutiveOver > CONSECUTIVE_OVER_THRESHOLD;
      const rollingMean = window.reduce((sum, d) => sum + d, 0) / window.length;
      const overRollingMean = window.length === ROLLING_WINDOW && rollingMean > FRAME_BUDGET_MS;

      if (overConsecutive || overRollingMean) {
        degraded = true;
        return true;
      }
      return false;
    },

    isDegraded(): boolean {
      return degraded;
    },
  };
}
