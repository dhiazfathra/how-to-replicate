import { describe, expect, it, vi } from 'vitest';
import { createCaptureStore } from './capture-store.js';
import type { CaptureRepository } from '../storage/repository.js';
import type { Capture } from '../types/capture.js';

function makeCapture(id: string, overrides: Partial<Capture> = {}): Capture {
  return {
    id,
    workspaceId: null,
    projectId: null,
    source: 'extension',
    state: 'recording',
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

function makeRepo(overrides: Partial<CaptureRepository> = {}): CaptureRepository {
  return {
    listCaptures: vi.fn(() => Promise.resolve([])),
    putCapture: vi.fn(() => Promise.resolve()),
    ...overrides,
  } as unknown as CaptureRepository;
}

describe('createCaptureStore', () => {
  it('hydrates captures from the repository', async () => {
    const capture = makeCapture('c1');
    const repo = makeRepo({ listCaptures: vi.fn(() => Promise.resolve([capture])) });
    const store = createCaptureStore(repo);
    await store.hydrate();
    expect(store.ids()).toEqual(['c1']);
    expect(store.capture('c1').get()).toEqual(capture);
  });

  it('is empty for an unknown capture id until hydrated/put', () => {
    const store = createCaptureStore(makeRepo());
    expect(store.capture('missing').get()).toBeUndefined();
  });

  it('applies a field mutation locally and synchronously before persistence resolves', async () => {
    const capture = makeCapture('c1');
    let resolvePut!: () => void;
    const putCapture = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolvePut = resolve;
        }),
    );
    const repo = makeRepo({ putCapture });
    const store = createCaptureStore(repo);
    store.putLocal(capture);

    const promise = store.setField('c1', 'state', 'redacting');
    // Synchronous local write already visible, before the persistence promise settles.
    expect(store.capture('c1').get()?.state).toBe('redacting');

    resolvePut();
    await promise;
    expect(putCapture).toHaveBeenCalledTimes(1);
  });

  it('notifies subscribers synchronously on a field mutation', async () => {
    const capture = makeCapture('c1');
    const store = createCaptureStore(makeRepo());
    store.putLocal(capture);
    const fn = vi.fn();
    store.capture('c1').subscribe(fn);
    await store.setField('c1', 'state', 'composing');
    expect(fn).toHaveBeenCalled();
  });

  it('throws when mutating a field on a capture that was never hydrated/put', async () => {
    const store = createCaptureStore(makeRepo());
    await expect(store.setField('missing', 'state', 'ready')).rejects.toThrow(/unknown capture/);
  });

  it('on persistence failure: keeps the local write, marks the field dirty, exposes persistError (no rollback)', async () => {
    const capture = makeCapture('c1');
    const failure = new Error('quota exceeded');
    const repo = makeRepo({ putCapture: vi.fn(() => Promise.reject(failure)) });
    const store = createCaptureStore(repo);
    store.putLocal(capture);

    await store.setField('c1', 'state', 'redacting');

    const stored = store.capture('c1').get();
    expect(stored?.state).toBe('redacting'); // NOT rolled back
    expect(stored?.sync.dirtyFields).toContain('state');
    expect(store.persistError('c1', 'state').get()).toBe(failure);
  });

  it('does not duplicate an already-dirty field on a second consecutive persistence failure', async () => {
    const capture = makeCapture('c1');
    const putCapture = vi.fn(() => Promise.reject(new Error('still failing')));
    const store = createCaptureStore(makeRepo({ putCapture }));
    store.putLocal(capture);

    await store.setField('c1', 'state', 'redacting');
    await store.setField('c1', 'state', 'composing');

    const stored = store.capture('c1').get();
    expect(stored?.sync.dirtyFields.filter((f) => f === 'state')).toHaveLength(1);
  });

  it('clears a prior persistError once a later write for the same field succeeds', async () => {
    const capture = makeCapture('c1');
    const putCapture = vi
      .fn()
      .mockRejectedValueOnce(new Error('transient'))
      .mockResolvedValueOnce(undefined);
    const store = createCaptureStore(makeRepo({ putCapture }));
    store.putLocal(capture);

    await store.setField('c1', 'state', 'redacting');
    expect(store.persistError('c1', 'state').get()).toBeInstanceOf(Error);

    await store.setField('c1', 'state', 'composing');
    expect(store.persistError('c1', 'state').get()).toBeUndefined();
  });

  it('wraps a non-Error rejection in an Error for persistError', async () => {
    const capture = makeCapture('c1');
    const putCapture = vi.fn(() => {
      // eslint-disable-next-line @typescript-eslint/prefer-promise-reject-errors -- exercising the non-Error rejection path deliberately
      return Promise.reject('nope');
    });
    const store = createCaptureStore(makeRepo({ putCapture }));
    store.putLocal(capture);

    await store.setField('c1', 'state', 'redacting');
    const error = store.persistError('c1', 'state').get();
    expect(error).toBeInstanceOf(Error);
    expect(error?.message).toBe('nope');
  });
});
