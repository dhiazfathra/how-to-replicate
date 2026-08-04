import { describe, expect, it } from 'vitest';
import { renderMarkdown } from './markdown.js';
import type { Capture, CaptureState } from '../types/capture.js';
import type { ReplicationDoc } from '../types/doc.js';

const doc: ReplicationDoc = {
  title: 'Checkout button throws TypeError',
  summary: '2 step(s) recorded deterministically.',
  steps: [
    { n: 1, text: 'Clicked "Checkout"', eventIds: ['ev-1'], tVideo: null },
    { n: 2, text: 'Navigated to /error', eventIds: ['ev-2'], tVideo: null },
  ],
  expected: 'Order confirmation page loads',
  actual: 'TypeError thrown, blank page',
  generator: 'deterministic',
  generatorModel: null,
};

function makeCapture(overrides: Partial<Capture> = {}): Capture {
  return {
    id: 'cap-1',
    workspaceId: null,
    projectId: null,
    source: 'extension',
    state: 'ready',
    fidelity: 'full',
    createdAt: '2026-08-04T00:00:00.000Z',
    epoch: 0,
    env: {
      userAgent: 'Mozilla/5.0 test',
      platform: 'MacIntel',
      viewport: { w: 1280, h: 800 },
      devicePixelRatio: 2,
      locale: 'en-US',
      timezone: 'America/Los_Angeles',
      url: 'https://app.example.com/checkout',
    },
    metadata: {},
    doc,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
    ...overrides,
  };
}

describe('renderMarkdown', () => {
  it('renders title, summary, steps, expected/actual, and environment table', () => {
    const md = renderMarkdown(makeCapture());
    expect(md).toContain('# Checkout button throws TypeError');
    expect(md).toContain('2 step(s) recorded deterministically.');
    expect(md).toContain('1. Clicked "Checkout" (events: `ev-1`)');
    expect(md).toContain('2. Navigated to /error (events: `ev-2`)');
    expect(md).toContain('Order confirmation page loads');
    expect(md).toContain('TypeError thrown, blank page');
    expect(md).toContain('| URL | https://app.example.com/checkout |');
    expect(md).toContain('| Viewport | 1280x800 |');
    expect(md).not.toContain('degraded');
  });

  it('includes the degraded fidelity notice and withheld count', () => {
    const md = renderMarkdown(
      makeCapture({ fidelity: 'degraded', withheldEventCount: 4 }),
    );
    expect(md).toContain('Fidelity: degraded');
    expect(md).toContain('4 events withheld by redaction policy.');
  });

  it('escapes a pipe in an environment value so it cannot split the table', () => {
    const md = renderMarkdown(
      makeCapture({
        env: {
          userAgent: 'test-agent',
          platform: 'test',
          viewport: { w: 100, h: 100 },
          devicePixelRatio: 1,
          locale: 'en-US',
          timezone: 'UTC',
          url: 'https://example.com/search?q=a|b',
        },
      }),
    );
    expect(md).toContain('https://example.com/search?q=a\\|b');
    expect(md).not.toContain('q=a|b |');
  });

  it('falls back to placeholders for missing expected/actual', () => {
    const md = renderMarkdown(
      makeCapture({ doc: { ...doc, expected: null, actual: null } }),
    );
    expect(md).toContain('_not recorded_');
  });

  it('throws if the capture has no document even when ready', () => {
    expect(() => renderMarkdown(makeCapture({ doc: null }))).toThrow(/no document/);
  });

  const nonReady: CaptureState[] = ['recording', 'redacting', 'composing', 'failed', 'expired'];
  it.each(nonReady)('throws when the capture is %s', (state) => {
    expect(() => renderMarkdown(makeCapture({ state }))).toThrow(/not ready/);
  });
});
