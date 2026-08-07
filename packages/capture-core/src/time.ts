export type PerformanceLike = {
  timeOrigin: number;
  now(): number;
};

export type Clock = {
  /** Wall-clock-anchored ms at construction time. Never compared directly — see below. */
  epoch: number;
  /** Ms offset from `epoch`, monotonic per the injected `PerformanceLike`. */
  now(): number;
};

/**
 * Create a clock anchored to `performance.timeOrigin + performance.now()` at
 * construction. All later timestamps are ms offsets from that epoch, never
 * wall-clock values — see plan conventions on why offsets, not wall time.
 */
export function createClock(perf: PerformanceLike = performance): Clock {
  const start = perf.now();
  const epoch = perf.timeOrigin + start;
  return {
    epoch,
    now(): number {
      // A backdated/adjusted PerformanceLike could report `now()` before
      // `start` (e.g. a stubbed clock in tests). Offsets are never negative;
      // clamp instead of letting a negative offset corrupt event ordering.
      const offset = perf.now() - start;
      return offset < 0 ? 0 : offset;
    },
  };
}
