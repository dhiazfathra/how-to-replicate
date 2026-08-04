import { describe, expect, it } from 'vitest';
import { compositeFrame, validateRegions, type DrawTarget } from './blur.js';

function fakeCtx(): DrawTarget & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    filter: 'none',
    drawImage: (): void => void calls.push('drawImage'),
    save: (): void => void calls.push('save'),
    restore: (): void => void calls.push('restore'),
    beginPath: (): void => void calls.push('beginPath'),
    rect: (): void => void calls.push('rect'),
    clip: (): void => void calls.push('clip'),
  };
}

describe('validateRegions', () => {
  it('returns a copy of well-formed rects', () => {
    const rects = [{ x: 1, y: 2, width: 3, height: 4 }];
    const result = validateRegions(rects);
    expect(result).toEqual(rects);
    expect(result).not.toBe(rects);
  });

  it('accepts an empty region list', () => {
    expect(validateRegions([])).toEqual([]);
  });

  it.each([
    { x: NaN, y: 0, width: 1, height: 1 },
    { x: 0, y: Infinity, width: 1, height: 1 },
    { x: 0, y: 0, width: NaN, height: 1 },
    { x: 0, y: 0, width: 1, height: NaN },
    { x: 0, y: 0, width: -1, height: 1 },
    { x: 0, y: 0, width: 1, height: -1 },
  ])('rejects a malformed rect %o', (rect) => {
    expect(validateRegions([rect])).toBeNull();
  });
});

describe('compositeFrame', () => {
  it('draws the full frame unblurred, then each rect clipped and blurred on top', () => {
    const ctx = fakeCtx();
    compositeFrame(ctx, 'source', 100, 50, [{ x: 0, y: 0, width: 10, height: 10 }]);

    expect(ctx.calls).toEqual(['drawImage', 'save', 'beginPath', 'rect', 'clip', 'drawImage', 'restore']);
    expect(ctx.filter).toBe('blur(24px)');
  });

  it('draws only the base frame when there are no rects', () => {
    const ctx = fakeCtx();
    compositeFrame(ctx, 'source', 100, 50, []);

    expect(ctx.calls).toEqual(['drawImage']);
    expect(ctx.filter).toBe('none');
  });
});
