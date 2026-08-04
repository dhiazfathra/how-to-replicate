import { describe, expect, it } from 'vitest';
import type { CaptureEvent, InteractionPayload } from '../types/event.js';
import { reduceNoise } from './noise.js';

function interaction(
  id: string,
  t: number,
  payload: Partial<InteractionPayload>,
): CaptureEvent {
  return {
    id,
    captureId: 'cap-1',
    t,
    kind: 'interaction',
    payload: {
      type: 'click',
      targetName: 'Save changes',
      targetSelector: '#save-btn',
      url: '/patients/123/edit',
      value: null,
      ...payload,
    },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

describe('reduceNoise', () => {
  it('returns nothing for an empty timeline', () => {
    expect(reduceNoise([])).toEqual([]);
  });

  it('drops mousemove events entirely', () => {
    const events = [
      interaction('e1', 0, { type: 'mousemove' }),
      interaction('e2', 10, { type: 'mousemove' }),
    ];
    expect(reduceNoise(events)).toEqual([]);
  });

  it('coalesces consecutive scrolls within the 500ms window', () => {
    const events = [
      interaction('e1', 0, { type: 'scroll' }),
      interaction('e2', 200, { type: 'scroll' }),
      interaction('e3', 500, { type: 'scroll' }),
    ];
    const groups = reduceNoise(events);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.events.map((e) => e.id)).toEqual(['e1', 'e2', 'e3']);
  });

  it('does not coalesce scrolls past the 500ms window', () => {
    const events = [
      interaction('e1', 0, { type: 'scroll' }),
      interaction('e2', 501, { type: 'scroll' }),
    ];
    const groups = reduceNoise(events);
    expect(groups).toHaveLength(2);
  });

  it('folds identical clicks (same selector) within the 1000ms window', () => {
    const events = [
      interaction('e1', 0, { type: 'click', targetSelector: '#save-btn' }),
      interaction('e2', 400, { type: 'click', targetSelector: '#save-btn' }),
      interaction('e3', 1000, { type: 'click', targetSelector: '#save-btn' }),
    ];
    const groups = reduceNoise(events);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.events).toHaveLength(3);
  });

  it('does not fold clicks past the 1000ms window', () => {
    const events = [
      interaction('e1', 0, { type: 'click', targetSelector: '#save-btn' }),
      interaction('e2', 1001, { type: 'click', targetSelector: '#save-btn' }),
    ];
    const groups = reduceNoise(events);
    expect(groups).toHaveLength(2);
  });

  it('does not fold clicks on different targets even within the window', () => {
    const events = [
      interaction('e1', 0, { type: 'click', targetSelector: '#save-btn' }),
      interaction('e2', 100, { type: 'click', targetSelector: '#cancel-btn' }),
    ];
    const groups = reduceNoise(events);
    expect(groups).toHaveLength(2);
  });

  it('never coalesces non-scroll, non-click interaction types', () => {
    const events = [
      interaction('e1', 0, { type: 'input' }),
      interaction('e2', 10, { type: 'input' }),
    ];
    const groups = reduceNoise(events);
    expect(groups).toHaveLength(2);
  });
});
