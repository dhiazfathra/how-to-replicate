import { describe, expect, it } from 'vitest';
import { createFrameBudgetMonitor, FRAME_BUDGET_MS, ROLLING_WINDOW } from './frame-budget.js';

describe('createFrameBudgetMonitor', () => {
  it('stays not-degraded for frames under budget', () => {
    const monitor = createFrameBudgetMonitor();
    for (let i = 0; i < 50; i += 1) {
      expect(monitor.record(FRAME_BUDGET_MS - 1)).toBe(false);
    }
    expect(monitor.isDegraded()).toBe(false);
  });

  it('degrades on the 4th consecutive over-budget frame, exactly once', () => {
    const monitor = createFrameBudgetMonitor();
    expect(monitor.record(20)).toBe(false);
    expect(monitor.record(20)).toBe(false);
    expect(monitor.record(20)).toBe(false);
    expect(monitor.record(20)).toBe(true);
    expect(monitor.isDegraded()).toBe(true);
    // Never re-triggers, never re-enables.
    expect(monitor.record(20)).toBe(false);
    expect(monitor.record(0)).toBe(false);
    expect(monitor.isDegraded()).toBe(true);
  });

  it('resets the consecutive-over streak on an in-budget frame', () => {
    const monitor = createFrameBudgetMonitor();
    monitor.record(20);
    monitor.record(20);
    monitor.record(20);
    expect(monitor.record(1)).toBe(false); // streak reset, not yet degraded
    expect(monitor.record(20)).toBe(false);
    expect(monitor.record(20)).toBe(false);
    expect(monitor.record(20)).toBe(false);
    expect(monitor.record(20)).toBe(true);
  });

  it('degrades once the rolling mean over the window exceeds budget, without consecutive breaches', () => {
    const monitor = createFrameBudgetMonitor();
    // Alternate over/under so the consecutive-over streak never exceeds 1,
    // but keep the mean above budget across the full window.
    let degraded = false;
    for (let i = 0; i < ROLLING_WINDOW; i += 1) {
      const duration = i % 2 === 0 ? FRAME_BUDGET_MS + 10 : FRAME_BUDGET_MS - 5;
      degraded = monitor.record(duration);
    }
    expect(degraded).toBe(true);
    expect(monitor.isDegraded()).toBe(true);
  });

  it('does not degrade from the rolling mean before the window fills, but the consecutive-over check still fires first', () => {
    const monitor = createFrameBudgetMonitor();
    for (let i = 0; i < ROLLING_WINDOW - 1; i += 1) {
      expect(monitor.record(FRAME_BUDGET_MS + 100)).toBe(i === 3);
    }
  });
});
