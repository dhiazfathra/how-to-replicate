import { describe, expect, it } from 'vitest';
import { transition } from './machine.js';
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

describe('transition', () => {
  it('allows recording -> redacting', () => {
    const { capture, event } = transition(makeCapture('recording'), 'redacting');
    expect(capture.state).toBe('redacting');
    expect(event.kind).toBe('lifecycle');
    expect(event.payload).toMatchObject({ transition: 'recording->redacting' });
  });

  it('allows redacting -> composing', () => {
    expect(transition(makeCapture('redacting'), 'composing').capture.state).toBe('composing');
  });

  it('allows redacting -> failed', () => {
    expect(transition(makeCapture('redacting'), 'failed').capture.state).toBe('failed');
  });

  it('allows composing -> ready', () => {
    expect(transition(makeCapture('composing'), 'ready').capture.state).toBe('ready');
  });

  it('allows composing -> failed', () => {
    expect(transition(makeCapture('composing'), 'failed').capture.state).toBe('failed');
  });

  it('allows ready -> expired', () => {
    expect(transition(makeCapture('ready'), 'expired').capture.state).toBe('expired');
  });

  it('carries an optional detail through the lifecycle event', () => {
    const { event } = transition(makeCapture('composing'), 'failed', 'doc-gen crashed');
    expect(event.payload).toMatchObject({ detail: 'doc-gen crashed' });
  });

  const illegal: [CaptureState, CaptureState][] = [
    ['recording', 'composing'],
    ['recording', 'ready'],
    ['recording', 'failed'],
    ['recording', 'expired'],
    ['recording', 'recording'],
    ['redacting', 'recording'],
    ['redacting', 'ready'],
    ['redacting', 'expired'],
    ['composing', 'recording'],
    ['composing', 'redacting'],
    ['composing', 'expired'],
    ['ready', 'recording'],
    ['ready', 'redacting'],
    ['ready', 'composing'],
    ['ready', 'failed'],
    ['failed', 'ready'],
    ['failed', 'recording'],
    ['failed', 'expired'],
    ['expired', 'ready'],
    ['expired', 'recording'],
    ['expired', 'failed'],
  ];

  it.each(illegal)('throws on %s -> %s', (from, to) => {
    expect(() => transition(makeCapture(from), to)).toThrow(/illegal capture transition/);
  });
});
