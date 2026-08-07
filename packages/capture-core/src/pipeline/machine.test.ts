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

  it('defaults the lifecycle event t to 0 when no clock offset is given', () => {
    const { event } = transition(makeCapture('recording'), 'redacting');
    expect(event.t).toBe(0);
  });

  it('stamps the lifecycle event with the given clock offset', () => {
    const { event } = transition(makeCapture('recording'), 'redacting', null, 4200);
    expect(event.t).toBe(4200);
  });

  const ALL_STATES: CaptureState[] = [
    'recording',
    'redacting',
    'composing',
    'ready',
    'failed',
    'expired',
  ];
  const LEGAL = new Set([
    'recording->redacting',
    'redacting->composing',
    'redacting->failed',
    'composing->ready',
    'composing->failed',
    'ready->expired',
  ]);

  // Full 6x6 cross-product minus the 6 legal pairs above, so this table
  // self-maintains if a state is ever added instead of needing hand-upkeep.
  const illegal = ALL_STATES.flatMap((from) =>
    ALL_STATES.filter((to) => !LEGAL.has(`${from}->${to}`)).map(
      (to): [CaptureState, CaptureState] => [from, to],
    ),
  );

  it('covers every non-legal state pair', () => {
    expect(illegal.length).toBe(ALL_STATES.length * ALL_STATES.length - LEGAL.size);
  });

  it.each(illegal)('throws on %s -> %s', (from, to) => {
    expect(() => transition(makeCapture(from), to)).toThrow(/illegal capture transition/);
  });
});
