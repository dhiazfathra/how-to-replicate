import { describe, expect, it, vi } from 'vitest';
import { selectEvictionCandidate, runEviction } from './eviction.js';
import type { Capture } from '../types/capture.js';
import type { CaptureRepository } from './repository.js';

function makeCapture(overrides: Partial<Capture> & { id: string }): Capture {
  return {
    workspaceId: null,
    projectId: null,
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
    doc: null,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
    ...overrides,
  };
}

describe('selectEvictionCandidate', () => {
  it('never evicts an unsynced capture, even as the sole candidate', () => {
    const capture = makeCapture({
      id: 'a',
      sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
    });
    expect(selectEvictionCandidate([capture])).toBeUndefined();
  });

  it('does not evict a capture with a recent lastPushedAt but manifestComplete: false', () => {
    // The named trap: a fresh-looking push timestamp must not substitute for
    // manifest completion (invariant 8).
    const stillUploading = makeCapture({
      id: 'uploading',
      sync: {
        revision: 5,
        lastPushedAt: '2026-08-07T00:00:00.000Z',
        manifestComplete: false,
        dirtyFields: [],
      },
    });
    expect(selectEvictionCandidate([stillUploading])).toBeUndefined();
  });

  it('picks the least-recently-pushed capture among eligible ones (LRU)', () => {
    const newer = makeCapture({
      id: 'newer',
      sync: {
        revision: 1,
        lastPushedAt: '2026-08-05T00:00:00.000Z',
        manifestComplete: true,
        dirtyFields: [],
      },
    });
    const older = makeCapture({
      id: 'older',
      sync: {
        revision: 1,
        lastPushedAt: '2026-08-01T00:00:00.000Z',
        manifestComplete: true,
        dirtyFields: [],
      },
    });
    const middle = makeCapture({
      id: 'middle',
      sync: {
        revision: 1,
        lastPushedAt: '2026-08-03T00:00:00.000Z',
        manifestComplete: true,
        dirtyFields: [],
      },
    });
    const ineligible = makeCapture({
      id: 'ineligible',
      sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
    });
    // Order exercises both reduce outcomes: older replaces newer as the
    // running oldest, then middle (not older than older) leaves it in place.
    expect(selectEvictionCandidate([newer, older, middle, ineligible])?.id).toBe('older');
  });

  it('falls back to createdAt when an eligible capture lacks lastPushedAt', () => {
    // A second eligible capture forces the reducer to actually run
    // pushedAtMillis on both sides of the comparison.
    const noPushTimestamp = makeCapture({
      id: 'a',
      createdAt: '2020-01-01T00:00:00.000Z',
      sync: { revision: 1, lastPushedAt: null, manifestComplete: true, dirtyFields: [] },
    });
    const pushedLater = makeCapture({
      id: 'b',
      sync: {
        revision: 1,
        lastPushedAt: '2026-08-01T00:00:00.000Z',
        manifestComplete: true,
        dirtyFields: [],
      },
    });
    expect(selectEvictionCandidate([noPushTimestamp, pushedLater])?.id).toBe('a');
  });

  it('skips a capture whose assets were already evicted', () => {
    const capture = makeCapture({
      id: 'a',
      localAssets: false,
      sync: {
        revision: 1,
        lastPushedAt: '2026-08-01T00:00:00.000Z',
        manifestComplete: true,
        dirtyFields: [],
      },
    });
    expect(selectEvictionCandidate([capture])).toBeUndefined();
  });

  it('returns undefined for an empty list', () => {
    expect(selectEvictionCandidate([])).toBeUndefined();
  });
});

describe('runEviction', () => {
  function mockRepo(): { repo: CaptureRepository; evictLocalAssets: ReturnType<typeof vi.fn> } {
    const evictLocalAssets = vi.fn().mockResolvedValue(undefined);
    return { repo: { evictLocalAssets } as unknown as CaptureRepository, evictLocalAssets };
  }

  it('is a no-op when the budget check passed (no pressure)', async () => {
    const { repo, evictLocalAssets } = mockRepo();
    const result = await runEviction(repo, [], { ok: true });
    expect(result).toEqual({ evictedCaptureId: null });
    expect(evictLocalAssets).not.toHaveBeenCalled();
  });

  it('falls back to refusal (no eviction) when nothing is eligible, even under pressure', async () => {
    const { repo, evictLocalAssets } = mockRepo();
    const unsynced = makeCapture({ id: 'a' });
    const result = await runEviction(repo, [unsynced], { ok: false, reason: 'capture-cap' });
    expect(result).toEqual({ evictedCaptureId: null });
    expect(evictLocalAssets).not.toHaveBeenCalled();
  });

  it('evicts the LRU-eligible capture under pressure', async () => {
    const { repo, evictLocalAssets } = mockRepo();
    const eligible = makeCapture({
      id: 'a',
      sync: {
        revision: 1,
        lastPushedAt: '2026-08-01T00:00:00.000Z',
        manifestComplete: true,
        dirtyFields: [],
      },
    });
    const result = await runEviction(repo, [eligible], { ok: false, reason: 'quota' });
    expect(result).toEqual({ evictedCaptureId: 'a' });
    expect(evictLocalAssets).toHaveBeenCalledWith('a');
  });
});
