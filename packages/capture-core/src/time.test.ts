import { describe, expect, it } from 'vitest';
import { createClock, type PerformanceLike } from './time.js';

function fakePerf(timeOrigin: number, ...nowValues: number[]): PerformanceLike {
  let i = 0;
  return {
    timeOrigin,
    now: () => {
      const v = nowValues[Math.min(i, nowValues.length - 1)] as number;
      i += 1;
      return v;
    },
  };
}

describe('createClock', () => {
  it('anchors epoch to timeOrigin + now() at construction', () => {
    const clock = createClock(fakePerf(1_000, 50));
    expect(clock.epoch).toBe(1_050);
  });

  it('returns the ms offset from construction time', () => {
    const clock = createClock(fakePerf(1_000, 50, 80));
    expect(clock.now()).toBe(30);
  });

  it('clamps a negative offset to 0', () => {
    const clock = createClock(fakePerf(1_000, 50, 10));
    expect(clock.now()).toBe(0);
  });

  it('returns exactly 0 when now() has not advanced', () => {
    const clock = createClock(fakePerf(1_000, 50, 50));
    expect(clock.now()).toBe(0);
  });

  it('defaults to the ambient performance object', () => {
    const clock = createClock();
    expect(clock.epoch).toBeGreaterThan(0);
    expect(clock.now()).toBeGreaterThanOrEqual(0);
  });
});
