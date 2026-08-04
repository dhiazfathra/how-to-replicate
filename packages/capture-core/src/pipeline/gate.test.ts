import { describe, expect, it } from 'vitest';
import { assertReady, isViewable } from './gate.js';
import type { Capture, CaptureState } from '../types/capture.js';

function makeCapture(state: CaptureState): Capture {
  return {
    id: 'cap-1',
    workspaceId: null,
    projectId: null,
    source: 'extension',
    state,
    fidelity: 'full',
    createdAt: '2026-08-04T00:00:00.000Z',
    epoch: 0,
    env: {
      userAgent: 'test-agent',
      platform: 'test',
      viewport: { w: 100, h: 100 },
      devicePixelRatio: 1,
      locale: 'en-US',
      timezone: 'UTC',
      url: 'https://example.com',
    },
    metadata: {},
    doc: null,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
  };
}

describe('isViewable', () => {
  it('is true only for ready', () => {
    expect(isViewable(makeCapture('ready'))).toBe(true);
  });

  const nonReady: CaptureState[] = ['recording', 'redacting', 'composing', 'failed', 'expired'];
  it.each(nonReady)('is false for %s', (state) => {
    expect(isViewable(makeCapture(state))).toBe(false);
  });
});

describe('assertReady', () => {
  it('does not throw for ready', () => {
    expect(() => assertReady(makeCapture('ready'))).not.toThrow();
  });

  it('throws for non-ready states', () => {
    expect(() => assertReady(makeCapture('composing'))).toThrow(/not ready/);
  });
});
