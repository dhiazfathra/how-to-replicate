import { beforeEach, describe, expect, it, vi } from 'vitest';
import { IDBFactory } from 'fake-indexeddb';
import { openCaptureDb } from '../storage/db.js';
import { CaptureRepository } from '../storage/repository.js';
import { finalizeCapture } from './run.js';
import { isViewable } from './gate.js';
import * as engineModule from '../redaction/engine.js';
import type { Capture } from '../types/capture.js';
import type { CaptureEvent } from '../types/event.js';
import type { InstantReplay } from '../buffer/instant-replay.js';

function makeCapture(state: Capture['state'] = 'composing'): Capture {
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

function makeEvent(id: string): CaptureEvent {
  return {
    id,
    captureId: 'cap-1',
    t: 0,
    kind: 'navigation',
    payload: { from: null, to: 'https://example.com', trigger: 'load' },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function fakeBuffer(events: CaptureEvent[], withheld: number, evicted = 0): InstantReplay {
  return {
    ingest: () => undefined,
    withheldEventCount: () => withheld,
    evictedCount: () => evicted,
    events: () => events,
    pushVideoChunk: () => undefined,
    videoChunks: () => [],
    pushScreenshot: () => undefined,
    screenshots: () => [],
  };
}

async function makeRepo(): Promise<CaptureRepository> {
  const db = await openCaptureDb(`test-${Math.random()}`, { indexedDB: new IDBFactory() });
  return new CaptureRepository(db);
}

describe('finalizeCapture', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('lands a clean capture in ready with a generated document', async () => {
    const repo = await makeRepo();
    const events = [makeEvent('ev-1')];
    const buffer = fakeBuffer(events, 0);

    const result = await finalizeCapture({ capture: makeCapture(), buffer, repo });

    expect(result.state).toBe('ready');
    expect(result.fidelity).toBe('full');
    expect(result.withheldEventCount).toBe(0);
    expect(result.doc).not.toBeNull();
    expect(isViewable(result)).toBe(true);
  });

  it('reaches ready with withheldEventCount matching the ingest drop count even when events were dropped', async () => {
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 3);

    const result = await finalizeCapture({ capture: makeCapture(), buffer, repo });

    expect(result.state).toBe('ready');
    expect(result.withheldEventCount).toBe(3);
    expect(result.fidelity).toBe('degraded');
  });

  it('folds ring-buffer evictions into withheldEventCount/fidelity too, not just redaction drops', async () => {
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 0, 2);

    const result = await finalizeCapture({ capture: makeCapture(), buffer, repo });

    expect(result.state).toBe('ready');
    expect(result.withheldEventCount).toBe(2);
    expect(result.fidelity).toBe('degraded');
  });

  it('sums redaction drops and ring-buffer evictions into a single withheldEventCount', async () => {
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 3, 2);

    const result = await finalizeCapture({ capture: makeCapture(), buffer, repo });

    expect(result.withheldEventCount).toBe(5);
    expect(result.fidelity).toBe('degraded');
  });

  it('never invokes a redactor: no parameter for one exists and createRedactor is never called', async () => {
    const spy = vi.spyOn(engineModule, 'createRedactor');
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 0);

    await finalizeCapture({ capture: makeCapture(), buffer, repo });

    expect(spy).not.toHaveBeenCalled();
    // @ts-expect-error - redactor/ruleset must not be part of the signature
    await finalizeCapture({ capture: makeCapture(), buffer, repo, redactor: {} });
  });

  it('lands in failed and not viewable when persistence is fatal', async () => {
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 0);
    vi.spyOn(repo, 'appendEvents').mockRejectedValueOnce(new Error('disk full'));

    const result = await finalizeCapture({ capture: makeCapture(), buffer, repo });

    expect(result.state).toBe('failed');
    expect(isViewable(result)).toBe(false);
  });

  it('lands in failed when the document generator crashes', async () => {
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 0);
    const docGenerator = vi.fn(() => {
      throw new Error('doc-gen crashed');
    });

    const result = await finalizeCapture({ capture: makeCapture(), buffer, repo, docGenerator });

    expect(result.state).toBe('failed');
    expect(isViewable(result)).toBe(false);
  });

  it('lands in failed when the buffer cannot be read', async () => {
    const repo = await makeRepo();
    const buffer: InstantReplay = {
      ...fakeBuffer([], 0),
      events: () => {
        throw new Error('buffer corrupted');
      },
    };

    const result = await finalizeCapture({ capture: makeCapture(), buffer, repo });

    expect(result.state).toBe('failed');
    expect(isViewable(result)).toBe(false);
  });

  it('lands in failed with a stringified reason when a non-Error is thrown', async () => {
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 0);
    const docGenerator = vi.fn(() => {
      // eslint-disable-next-line @typescript-eslint/only-throw-error
      throw 'plain string failure';
    });

    const result = await finalizeCapture({ capture: makeCapture(), buffer, repo, docGenerator });

    expect(result.state).toBe('failed');
  });

  it('does not throw even if the best-effort failure persistence itself rejects', async () => {
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 0);
    vi.spyOn(repo, 'appendEvents').mockRejectedValue(new Error('disk full'));
    vi.spyOn(repo, 'putCapture').mockRejectedValue(new Error('also disk full'));

    const result = await finalizeCapture({ capture: makeCapture(), buffer, repo });

    expect(result.state).toBe('failed');
  });

  const nonComposing: Capture['state'][] = ['recording', 'redacting', 'ready', 'failed', 'expired'];
  it.each(nonComposing)(
    'throws immediately (never a rejected pipeline) when called with a %s capture',
    async (state) => {
      const repo = await makeRepo();
      const buffer = fakeBuffer([makeEvent('ev-1')], 0);

      // Asserts the upfront precondition, not a transition-table throw from
      // inside the catch block: without it, a non-composing capture would
      // hit `transition(composed, 'ready')` throwing, then the catch's own
      // `transition(capture, 'failed')` throwing a second time for states
      // (e.g. recording) where `failed` isn't reachable either.
      await expect(finalizeCapture({ capture: makeCapture(state), buffer, repo })).rejects.toThrow(
        /requires a composing capture/,
      );
    },
  );

  it('stamps the ready lifecycle event with the injected clock offset', async () => {
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 0);
    const clock = { epoch: 0, now: () => 9001 };

    await finalizeCapture({ capture: makeCapture(), buffer, repo, clock });

    const [lifecycleEvent] = await repo.readEvents('cap-1').then((events) =>
      events.filter((e) => e.kind === 'lifecycle'),
    );
    expect(lifecycleEvent?.t).toBe(9001);
  });

  it('stamps the failed lifecycle event with the injected clock offset', async () => {
    const repo = await makeRepo();
    const buffer = fakeBuffer([makeEvent('ev-1')], 0);
    const clock = { epoch: 0, now: () => 4242 };
    vi.spyOn(repo, 'appendEvents').mockRejectedValueOnce(new Error('disk full'));

    await finalizeCapture({ capture: makeCapture(), buffer, repo, clock });

    // appendEvents was mocked out for the first (events) call only, so the
    // failure-path appendEvents([event]) call after it went through for real.
    const stored = await repo.readEvents('cap-1');
    const lifecycleEvent = stored.find((e) => e.kind === 'lifecycle');
    expect(lifecycleEvent?.t).toBe(4242);
  });
});
