import { describe, expect, it, vi } from 'vitest';
import {
  CAPTURE_START_MESSAGE_TYPE,
  CAPTURE_STOP_MESSAGE_TYPE,
  createAnchoredClock,
  createSessionListener,
} from './session.js';

describe('createSessionListener', () => {
  it('routes a well-formed start message to onStart', () => {
    const onStart = vi.fn();
    const onStop = vi.fn();
    const listener = createSessionListener({ onStart, onStop });

    const message = {
      type: CAPTURE_START_MESSAGE_TYPE,
      captureId: 'cap-1',
      epoch: 100,
      blurSelectors: ['.ssn'],
    };
    listener(message);

    expect(onStart).toHaveBeenCalledWith(message);
    expect(onStop).not.toHaveBeenCalled();
  });

  it('routes a well-formed stop message to onStop', () => {
    const onStart = vi.fn();
    const onStop = vi.fn();
    const listener = createSessionListener({ onStart, onStop });

    const message = { type: CAPTURE_STOP_MESSAGE_TYPE, captureId: 'cap-1' };
    listener(message);

    expect(onStop).toHaveBeenCalledWith(message);
    expect(onStart).not.toHaveBeenCalled();
  });

  it('ignores malformed or unrelated messages', () => {
    const onStart = vi.fn();
    const onStop = vi.fn();
    const listener = createSessionListener({ onStart, onStop });

    listener(null);
    listener('a string');
    listener({ type: CAPTURE_START_MESSAGE_TYPE, captureId: 'cap-1' }); // missing epoch/blurSelectors
    listener({ type: CAPTURE_START_MESSAGE_TYPE, captureId: 1, epoch: 1, blurSelectors: [] });
    listener({ type: CAPTURE_START_MESSAGE_TYPE, captureId: 'cap-1', epoch: 1, blurSelectors: 'nope' });
    listener({ type: 'htr:unrelated', captureId: 'cap-1' });

    expect(onStart).not.toHaveBeenCalled();
    expect(onStop).not.toHaveBeenCalled();
  });
});

describe('createAnchoredClock', () => {
  it('reports elapsed offset since the shared epoch, not a fresh independent epoch', () => {
    const perf = { timeOrigin: 1_000, now: () => 50 };
    const clock = createAnchoredClock(1_020, perf);

    expect(clock.epoch).toBe(1_020);
    expect(clock.now()).toBe(30); // (1000 + 50) - 1020
  });

  it('clamps a negative offset to zero', () => {
    const perf = { timeOrigin: 1_000, now: () => 0 };
    const clock = createAnchoredClock(2_000, perf);

    expect(clock.now()).toBe(0);
  });
});
