import { describe, expect, it } from 'vitest';
import type { Capture, CaptureState, ReplicationDoc } from '@htr/capture-core';
import { buildIssueInput } from './router.js';

const doc: ReplicationDoc = {
  title: 'Repro: crash on submit',
  summary: 'The form crashes when submitted twice.',
  steps: [
    { n: 1, text: 'Click submit', eventIds: ['e1'], tVideo: 0 },
    { n: 2, text: 'Observe a network error', eventIds: ['e2'], tVideo: 1 },
  ],
  expected: 'Form submits once.',
  actual: 'App throws a network error.',
  generator: 'deterministic',
  generatorModel: null,
};

function makeCapture(overrides: Partial<Capture> = {}): Capture {
  return {
    id: 'cap-1',
    workspaceId: null,
    projectId: 'demo',
    source: 'extension',
    state: 'ready',
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
    doc,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
    ...overrides,
  };
}

describe('buildIssueInput', () => {
  it('builds an IssueInput from a ready capture, with a project label and deep link', () => {
    const input = buildIssueInput(makeCapture(), 'https://app.example.com/capture/cap-1');

    expect(input.title).toBe(doc.title);
    expect(input.body).toContain('1. Click submit');
    expect(input.body).toContain('Expected: Form submits once.');
    expect(input.body).toContain('Actual: App throws a network error.');
    expect(input.captureUrl).toBe('https://app.example.com/capture/cap-1');
    expect(input.labels).toEqual([
      { name: 'project:demo', source: 'project' },
      { name: 'error:network', source: 'error-signature' },
      { name: 'error:crash', source: 'error-signature' },
    ]);
  });

  const nonReady: CaptureState[] = ['recording', 'redacting', 'composing', 'failed', 'expired'];
  it.each(nonReady)('refuses a non-ready capture (%s)', (state) => {
    expect(() => buildIssueInput(makeCapture({ state }), 'https://x')).toThrow(/not ready/);
  });

  it('throws if a ready capture somehow has no doc', () => {
    expect(() => buildIssueInput(makeCapture({ doc: null }), 'https://x')).toThrow(/no replication doc/);
  });

  it('omits expected/actual lines when absent', () => {
    const input = buildIssueInput(
      makeCapture({ doc: { ...doc, expected: null, actual: null } }),
      'https://x',
    );
    expect(input.body).not.toContain('Expected:');
    expect(input.body).not.toContain('Actual:');
  });
});
