import { describe, expect, it, vi } from 'vitest';
import {
  BLUR_REGIONS_MESSAGE_TYPE,
  resolveBlurRegions,
  startBlurRegionTracking,
  type QueryableDocument,
} from './blur-regions.js';

function fakeDoc(bySelector: Record<string, { getBoundingClientRect(): { x: number; y: number; width: number; height: number } }[]>): QueryableDocument {
  return {
    querySelectorAll: (selector) => bySelector[selector] ?? [],
  };
}

describe('resolveBlurRegions', () => {
  it('collects rects across every selector match', () => {
    const doc = fakeDoc({
      '.ssn': [{ getBoundingClientRect: () => ({ x: 1, y: 2, width: 3, height: 4 }) }],
      '.name': [
        { getBoundingClientRect: () => ({ x: 5, y: 6, width: 7, height: 8 }) },
        { getBoundingClientRect: () => ({ x: 9, y: 10, width: 11, height: 12 }) },
      ],
    });
    const regions = resolveBlurRegions(doc, ['.ssn', '.name']);
    expect(regions).toHaveLength(3);
    expect(regions[0]).toEqual({ x: 1, y: 2, width: 3, height: 4 });
  });

  it('returns an empty array when no selector matches anything', () => {
    expect(resolveBlurRegions(fakeDoc({}), ['.missing'])).toEqual([]);
  });
});

describe('startBlurRegionTracking', () => {
  it('posts a blur-regions message every scheduled frame', () => {
    const doc = fakeDoc({ '.ssn': [{ getBoundingClientRect: () => ({ x: 0, y: 0, width: 1, height: 1 }) }] });
    const sendMessage = vi.fn();
    let scheduled: (() => void) | undefined;
    const scheduler = { requestFrame: (cb: () => void) => (scheduled = cb) };

    startBlurRegionTracking(doc, { sendMessage }, scheduler, 'cap-1', ['.ssn']);
    scheduled?.();
    expect(sendMessage).toHaveBeenCalledWith({
      type: BLUR_REGIONS_MESSAGE_TYPE,
      captureId: 'cap-1',
      regions: [{ x: 0, y: 0, width: 1, height: 1 }],
    });

    sendMessage.mockClear();
    scheduled?.();
    expect(sendMessage).toHaveBeenCalledTimes(1);
  });

  it('posts an empty-regions frame when nothing matches, and stop() halts further frames', () => {
    const doc = fakeDoc({});
    const sendMessage = vi.fn();
    let scheduled: (() => void) | undefined;
    const scheduler = { requestFrame: (cb: () => void) => (scheduled = cb) };

    const stop = startBlurRegionTracking(doc, { sendMessage }, scheduler, 'cap-1', []);
    scheduled?.();
    expect(sendMessage).toHaveBeenCalledWith({
      type: BLUR_REGIONS_MESSAGE_TYPE,
      captureId: 'cap-1',
      regions: [],
    });

    stop();
    sendMessage.mockClear();
    scheduled?.();
    expect(sendMessage).not.toHaveBeenCalled();
  });
});
