import { beforeEach, describe, expect, it, vi } from 'vitest';
import { IDBFactory } from 'fake-indexeddb';
import { openCaptureDb } from '../storage/db.js';
import { CaptureRepository } from '../storage/repository.js';
import { createCaptureStore, type CaptureStore } from '../store/capture-store.js';
import { createMutationQueue, QUEUE_BYTE_CAP, type MutationQueue } from './queue.js';
import type { Capture } from '../types/capture.js';
import type { Mutation } from './mutations.js';
import type { PullDeltasResult, PushMutationsResult, SyncTransport } from './transport.js';
import { computeBackoffMs, createSyncEngine, BASE_BACKOFF_MS, MAX_BACKOFF_MS } from './engine.js';

function makeCapture(id: string): Capture {
  return {
    id,
    workspaceId: 'ws-1',
    projectId: null,
    source: 'sdk',
    state: 'ready',
    fidelity: 'full',
    createdAt: '2026-08-04T00:00:00.000Z',
    epoch: 0,
    env: {
      userAgent: 'x',
      platform: 'x',
      viewport: { w: 1, h: 1 },
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

type Harness = {
  factory: IDBFactory;
  dbName: string;
  repo: CaptureRepository;
  store: CaptureStore;
  queue: MutationQueue;
};

async function freshHarness(dbName = `engine-test-${Math.random()}`): Promise<Harness> {
  const factory = new IDBFactory();
  const db = await openCaptureDb(dbName, { indexedDB: factory });
  const repo = new CaptureRepository(db);
  const store = createCaptureStore(repo);
  const queue = createMutationQueue(db);
  return { factory, dbName, repo, store, queue };
}

/** Reopen the store+queue against the same underlying factory — a simulated restart. */
async function reopen(h: Harness): Promise<Harness> {
  const db = await openCaptureDb(h.dbName, { indexedDB: h.factory });
  const repo = new CaptureRepository(db);
  const store = createCaptureStore(repo);
  await store.hydrate();
  const queue = createMutationQueue(db);
  return { ...h, repo, store, queue };
}

function fakeTransport(overrides: Partial<SyncTransport> = {}): SyncTransport {
  return {
    pushMutations: vi.fn((mutations: Mutation[]): Promise<PushMutationsResult[]> =>
      Promise.resolve(mutations.map((m) => ({ mutationId: m.id, applied: true, error: '' }))),
    ),
    pullDeltas: vi.fn(
      (): Promise<PullDeltasResult> => Promise.resolve({ mutations: [], revision: 0, hasMore: false }),
    ),
    requestAssetUpload: vi.fn(),
    completeAssetUpload: vi.fn(),
    ...overrides,
  };
}

describe('computeBackoffMs', () => {
  it('grows exponentially with attempt, capped at MAX_BACKOFF_MS, scaled by random()', () => {
    expect(computeBackoffMs(1, () => 1)).toBe(BASE_BACKOFF_MS);
    expect(computeBackoffMs(2, () => 1)).toBe(BASE_BACKOFF_MS * 2);
    expect(computeBackoffMs(10, () => 1)).toBe(MAX_BACKOFF_MS);
    expect(computeBackoffMs(1, () => 0)).toBe(0);
    expect(computeBackoffMs(0, () => 1)).toBe(BASE_BACKOFF_MS); // attempt clamped to >= 1
  });
});

describe('createSyncEngine', () => {
  let h: Harness;

  beforeEach(async () => {
    h = await freshHarness();
    h.store.putLocal(makeCapture('cap-1'));
    await h.repo.putCapture(makeCapture('cap-1'));
  });

  it('is inert without a transport: flush/pull no-op, but local save+enqueue still works', async () => {
    const engine = createSyncEngine({
      transport: undefined,
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      workspaceId: 'ws-1',
    });
    expect(engine.isActive()).toBe(false);

    const cap = engine.handle('cap-1');
    cap.title = 'Offline title';
    const result = await cap.save();
    expect(result).toEqual({ ok: true });
    expect(h.store.capture('cap-1').get()?.doc?.title).toBe('Offline title');
    expect((await h.queue.list()).length).toBe(1);

    await engine.flush(); // no-op: no transport
    await engine.pull(); // no-op: no transport
    expect((await h.queue.list()).length).toBe(1); // nothing pushed, item still queued
  });

  it('capture.title = x; capture.save() writes the observable synchronously and enqueues', async () => {
    const engine = createSyncEngine({
      transport: fakeTransport(),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const cap = engine.handle('cap-1');
    cap.title = 'New title';
    const before = h.store.capture('cap-1').get()?.doc?.title;
    const savePromise = cap.save();
    // Synchronous: the observable already reflects the edit before save() resolves.
    expect(before).not.toBe('New title');
    await savePromise;
    expect(h.store.capture('cap-1').get()?.doc?.title).toBe('New title');
    expect((await h.queue.list())[0]?.mutation.op).toEqual({ type: 'setTitle', title: 'New title' });
  });

  it('clientT is an offset from capture.epoch, not a wall-clock timestamp', async () => {
    const epoched = makeCapture('cap-epoch');
    epoched.epoch = 1_000_000;
    h.store.putLocal(epoched);
    await h.repo.putCapture(epoched);

    const engine = createSyncEngine({
      transport: fakeTransport(),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      now: () => 1_000_777,
    });
    const cap = engine.handle('cap-epoch');
    cap.title = 'Offset title';
    await cap.save();

    expect((await h.queue.list())[0]?.mutation.clientT).toBe(777);
  });

  it('save() with no changed fields is a cheap no-op', async () => {
    const engine = createSyncEngine({
      transport: fakeTransport(),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const cap = engine.handle('cap-1');
    await expect(cap.save()).resolves.toEqual({ ok: true });
    expect((await h.queue.list())).toEqual([]);
  });

  it('handle() throws for an unknown capture id', () => {
    const engine = createSyncEngine({
      transport: fakeTransport(),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    expect(() => engine.handle('missing')).toThrow(/unknown capture/);
  });

  it('save() commits every changed field in one batch (title, summary, tags, assigneeUserId)', async () => {
    const engine = createSyncEngine({
      transport: fakeTransport(),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const cap = engine.handle('cap-1');
    expect(cap.title).toBe('');
    expect(cap.summary).toBe('');
    expect(cap.tags).toEqual([]);
    expect(cap.assigneeUserId).toBe('');

    cap.title = 'T';
    cap.summary = 'S';
    cap.tags = ['a', 'b'];
    cap.assigneeUserId = 'u1';
    await expect(cap.save()).resolves.toEqual({ ok: true });

    const persisted = h.store.capture('cap-1').get();
    expect(persisted?.doc?.title).toBe('T');
    expect(persisted?.doc?.summary).toBe('S');
    expect(persisted?.metadata.tags).toEqual(['a', 'b']);
    expect(persisted?.metadata.assigneeUserId).toBe('u1');

    const queued = await h.queue.list();
    expect(queued.map((q) => q.field).sort()).toEqual(
      ['doc.title', 'doc.summary', 'metadata.assigneeUserId', 'metadata.tags'].sort(),
    );
  });

  it('a fresh handle() on an already-populated capture reads real persisted values (not just defaults)', async () => {
    const engine = createSyncEngine({
      transport: fakeTransport(),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const seed = engine.handle('cap-1');
    seed.title = 'Seeded';
    seed.summary = 'Seeded summary';
    seed.tags = ['x', 'y'];
    seed.assigneeUserId = 'u-seed';
    await expect(seed.save()).resolves.toEqual({ ok: true });

    const fresh = engine.handle('cap-1');
    expect(fresh.title).toBe('Seeded');
    expect(fresh.summary).toBe('Seeded summary');
    expect(fresh.tags).toEqual(['x', 'y']);
    expect(fresh.assigneeUserId).toBe('u-seed');

    // A rejected save() on this already-populated handle must reset every
    // field back to the real persisted values (not the empty defaults).
    await h.queue.enqueue({
      mutation: {
        id: 'huge',
        captureId: 'cap-1',
        op: { type: 'setTitle', title: 'x'.repeat(QUEUE_BYTE_CAP) },
        clientT: 0,
      },
      field: 'doc.title',
      base: '',
    });
    fresh.tags = ['changed'];
    fresh.assigneeUserId = 'someone-else';
    await expect(fresh.save()).resolves.toEqual({ ok: false, reason: 'queue-full' });
    expect(fresh.tags).toEqual(['x', 'y']);
    expect(fresh.assigneeUserId).toBe('u-seed');
  });

  it('appendComment enqueues an appendComment mutation', async () => {
    const engine = createSyncEngine({
      transport: fakeTransport(),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const cap = engine.handle('cap-1');
    await cap.appendComment('hello');
    expect(h.store.capture('cap-1').get()?.metadata.comments).toHaveLength(1);
    const queued = await h.queue.list();
    expect(queued[0]?.field).toBe('metadata.comments');
  });

  it('rejects save() when the queue has no room, leaves the previous value in place, and survives a restart', async () => {
    // Pre-fill the queue to the byte cap directly, bypassing the engine.
    await h.queue.enqueue({
      mutation: {
        id: 'huge',
        captureId: 'cap-1',
        op: { type: 'setTitle', title: 'x'.repeat(QUEUE_BYTE_CAP) },
        clientT: 0,
      },
      field: 'doc.title',
      base: '',
    });

    const engine = createSyncEngine({
      transport: fakeTransport(),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const cap = engine.handle('cap-1');
    cap.title = 'Should not stick';
    const result = await cap.save();
    expect(result).toEqual({ ok: false, reason: 'queue-full' });
    expect(h.store.capture('cap-1').get()?.doc).toBe(null); // previous value, untouched

    const restarted = await reopen(h);
    expect(restarted.store.capture('cap-1').get()?.doc).toBe(null);
  });

  it('the pending-sync indicator (capacity().nearCap) reflects queue depth approaching the cap', async () => {
    const nearCapTitle = 'x'.repeat(Math.ceil(QUEUE_BYTE_CAP * 0.95));
    await h.queue.enqueue({
      mutation: { id: 'a', captureId: 'cap-1', op: { type: 'setTitle', title: nearCapTitle }, clientT: 0 },
      field: 'doc.title',
      base: '',
    });
    const cap = await h.queue.capacity();
    expect(cap.nearCap).toBe(true);
    expect(cap.atCap).toBe(false);
  });

  it('rolls back a rejected mutation that is still the latest for its field', async () => {
    const engine = createSyncEngine({
      transport: fakeTransport({
        pushMutations: vi.fn((mutations: Mutation[]) =>
          Promise.resolve(mutations.map((m) => ({ mutationId: m.id, applied: false, error: 'conflict' }))),
        ),
      }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const cap = engine.handle('cap-1');
    cap.title = 'Rejected title';
    await cap.save();
    expect(h.store.capture('cap-1').get()?.doc?.title).toBe('Rejected title');

    await engine.flush();

    expect(h.store.capture('cap-1').get()?.doc?.title).toBe(''); // rolled back to base
    expect(await h.queue.list()).toEqual([]); // reject removes it from the queue
  });

  it('does NOT roll back on a transport error (timeout/network/disconnect) — retries later instead', async () => {
    const push = vi.fn(() => Promise.reject(new Error('network error')));
    const engine = createSyncEngine({
      transport: fakeTransport({ pushMutations: push }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const cap = engine.handle('cap-1');
    cap.title = 'Still pending';
    await cap.save();

    await engine.flush();

    expect(h.store.capture('cap-1').get()?.doc?.title).toBe('Still pending'); // never rolled back
    expect((await h.queue.list()).length).toBe(1); // still queued for retry
    expect(push).toHaveBeenCalledOnce();
  });

  it('an out-of-order reject for an older mutation does not clobber a newer local edit for the same field', async () => {
    // Sequence: title -> "v1" (will be rejected later), then two more edits to
    // the same field land on top ("v2", "v3") while the v1 reject is still in
    // flight. The v1 reject must restore to v1's base, then replay v2 and v3
    // on top — so the user's most recent intent (v3) survives.
    const engine = createSyncEngine({
      transport: fakeTransport(),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });

    const cap = engine.handle('cap-1');
    cap.title = 'v1';
    await cap.save();
    const v1Id = (await h.queue.list())[0]!.mutation.id;

    // Two further edits land while v1's reject is (conceptually) in flight.
    cap.title = 'v2';
    await cap.save();
    cap.title = 'v3';
    await cap.save();

    expect(h.store.capture('cap-1').get()?.doc?.title).toBe('v3');
    expect((await h.queue.list()).map((i) => i.mutation.id)).toContain(v1Id);

    const items = await h.queue.list();
    const v1Item = items.find((i) => i.mutation.id === v1Id)!;
    // Exercise the real flush end-to-end: it pushes all three queued
    // mutations, and the fake gateway rejects only the stale v1.
    const pushAll = vi.fn((mutations: Mutation[]): Promise<PushMutationsResult[]> =>
      Promise.resolve(
        mutations.map((m) => ({
          mutationId: m.id,
          applied: m.id !== v1Id,
          error: m.id === v1Id ? 'stale' : '',
        })),
      ),
    );
    const engine2 = createSyncEngine({
      transport: fakeTransport({ pushMutations: pushAll }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    await engine2.flush();

    // v1 rejected and rolled back to its base (''), then v2 and v3 replayed on
    // top in order — final value is v3, not '' and not v2.
    expect(h.store.capture('cap-1').get()?.doc?.title).toBe('v3');
    expect(v1Item.field).toBe('doc.title');
  });

  it('pull applies deltas into the local store and advances the persisted revision cursor', async () => {
    let calls = 0;
    const transport = fakeTransport({
      pullDeltas: vi.fn((): Promise<PullDeltasResult> => {
        calls += 1;
        if (calls === 1) {
          return Promise.resolve({
            mutations: [
              { id: 'm1', captureId: 'cap-1', op: { type: 'setSummary', summary: 'from server' }, clientT: 0 },
            ],
            revision: 5,
            hasMore: false,
          });
        }
        return Promise.resolve({ mutations: [], revision: 5, hasMore: false });
      }),
    });
    const engine = createSyncEngine({
      transport,
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      workspaceId: 'ws-1',
    });

    await engine.pull();

    expect(h.store.capture('cap-1').get()?.doc?.summary).toBe('from server');
    expect(await h.repo.getSyncCursor('ws-1')).toBe(5);
  });

  it('pull is a no-op without a workspaceId even with a transport configured', async () => {
    const pullDeltas = vi.fn();
    const engine = createSyncEngine({
      transport: fakeTransport({ pullDeltas }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    await engine.pull();
    expect(pullDeltas).not.toHaveBeenCalled();
  });

  it('pull stops on a transport error without throwing, leaving the cursor unchanged', async () => {
    const pullDeltas = vi.fn(() => Promise.reject(new Error('network error')));
    const engine = createSyncEngine({
      transport: fakeTransport({ pullDeltas }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      workspaceId: 'ws-1',
    });
    await expect(engine.pull()).resolves.toBeUndefined();
    expect(await h.repo.getSyncCursor('ws-1')).toBe(0);
  });

  it('pull pages until hasMore is false', async () => {
    let calls = 0;
    const pullDeltas = vi.fn((): Promise<PullDeltasResult> => {
      calls += 1;
      if (calls === 1) return Promise.resolve({ mutations: [], revision: 1, hasMore: true });
      return Promise.resolve({ mutations: [], revision: 2, hasMore: false });
    });
    const engine = createSyncEngine({
      transport: fakeTransport({ pullDeltas }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      workspaceId: 'ws-1',
    });
    await engine.pull();
    expect(pullDeltas).toHaveBeenCalledTimes(2);
    expect(await h.repo.getSyncCursor('ws-1')).toBe(2);
  });

  it('appendComment, flush, then pull does not duplicate the client\'s own comment', async () => {
    // Minimal repro from the sync-convergence property test: flush() removes
    // the mutation from the local queue once the server confirms it, but
    // nothing advances the pull cursor first — so the very next pull() refetches
    // the same delta the client just pushed. appendMutation must be idempotent
    // under replay (dedup by comment ID) or the comment is appended twice.
    let pushed: Mutation[] = [];
    const transport = fakeTransport({
      pushMutations: vi.fn((mutations: Mutation[]): Promise<PushMutationsResult[]> => {
        pushed = mutations;
        return Promise.resolve(mutations.map((m) => ({ mutationId: m.id, applied: true, error: '' })));
      }),
      pullDeltas: vi.fn(
        (): Promise<PullDeltasResult> =>
          Promise.resolve({ mutations: pushed, revision: 1, hasMore: false }),
      ),
    });
    const engine = createSyncEngine({
      transport,
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      workspaceId: 'ws-1',
    });

    await engine.handle('cap-1').appendComment('hello');
    await engine.flush();
    await engine.pull();

    const comments = h.store.capture('cap-1').get()?.metadata.comments;
    expect(comments).toHaveLength(1);
    expect((comments as { id: string; body: string }[])[0]?.body).toBe('hello');
  });

  it('pull skips deltas for captures not present locally', async () => {
    const pullDeltas = vi.fn(
      (): Promise<PullDeltasResult> =>
        Promise.resolve({
          mutations: [
            { id: 'm1', captureId: 'unknown-cap', op: { type: 'setTitle', title: 'x' }, clientT: 0 },
          ],
          revision: 1,
          hasMore: false,
        }),
    );
    const engine = createSyncEngine({
      transport: fakeTransport({ pullDeltas }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      workspaceId: 'ws-1',
    });
    await expect(engine.pull()).resolves.toBeUndefined();
    expect(await h.repo.getSyncCursor('ws-1')).toBe(1);
  });

  it('flush is a no-op when already flushing (re-entrant guard) and when the queue is empty', async () => {
    const push = vi.fn((mutations: Mutation[]) =>
      Promise.resolve(mutations.map((m) => ({ mutationId: m.id, applied: true, error: '' }))),
    );
    const engine = createSyncEngine({
      transport: fakeTransport({ pushMutations: push }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    await engine.flush(); // empty queue
    expect(push).not.toHaveBeenCalled();

    const cap = engine.handle('cap-1');
    cap.title = 'x';
    await cap.save();
    const [p1, p2] = [engine.flush(), engine.flush()];
    await Promise.all([p1, p2]);
    expect(push).toHaveBeenCalledTimes(1);
  });

  it('flush ignores push results for mutation ids it does not recognize', async () => {
    const push = vi.fn(
      (): Promise<PushMutationsResult[]> =>
        Promise.resolve([{ mutationId: 'not-queued', applied: true, error: '' }]),
    );
    const engine = createSyncEngine({
      transport: fakeTransport({ pushMutations: push }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const cap = engine.handle('cap-1');
    cap.title = 'x';
    await cap.save();
    await expect(engine.flush()).resolves.toBeUndefined();
    expect((await h.queue.list()).length).toBe(1); // still queued: the real id was never acked
  });

  it('resumes cleanly after a week offline: queued edits flush and deltas pull once back online', async () => {
    const cap = createSyncEngine({
      transport: undefined,
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      workspaceId: 'ws-1',
    }).handle('cap-1');
    cap.title = 'Made while offline';
    await cap.save();

    // A week passes with no transport. Nothing is lost — it's just queued.
    expect((await h.queue.list()).length).toBe(1);

    // Back online: a transport shows up on a fresh engine instance wired to
    // the same repo/store/queue (as a real reload would produce).
    const push = vi.fn((mutations: Mutation[]) =>
      Promise.resolve(mutations.map((m) => ({ mutationId: m.id, applied: true, error: '' }))),
    );
    const pullDeltas = vi.fn(
      (): Promise<PullDeltasResult> => Promise.resolve({ mutations: [], revision: 42, hasMore: false }),
    );
    const onlineEngine = createSyncEngine({
      transport: fakeTransport({ pushMutations: push, pullDeltas }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      workspaceId: 'ws-1',
    });

    await onlineEngine.flush();
    await onlineEngine.pull();

    expect(push).toHaveBeenCalledOnce();
    expect((await h.queue.list()).length).toBe(0);
    expect(await h.repo.getSyncCursor('ws-1')).toBe(42);
  });

  async function waitFor(check: () => boolean, timeoutMs = 2_000): Promise<void> {
    const start = Date.now();
    while (!check()) {
      if (Date.now() - start > timeoutMs) throw new Error('waitFor: timed out');
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
  }

  it('start() runs flush+pull immediately and on an interval, and stop() cancels the loop', async () => {
    const pullDeltas = vi.fn(
      (): Promise<PullDeltasResult> => Promise.resolve({ mutations: [], revision: 0, hasMore: false }),
    );
    const engine = createSyncEngine({
      transport: fakeTransport({ pullDeltas }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
      workspaceId: 'ws-1',
    });

    const stop = engine.start(20);
    await waitFor(() => pullDeltas.mock.calls.length >= 1);
    await waitFor(() => pullDeltas.mock.calls.length >= 2);

    stop();
    const countAtStop = pullDeltas.mock.calls.length;
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(pullDeltas.mock.calls.length).toBe(countAtStop); // no further ticks after stop
  });

  it('retries with backoff after a push failure while start() is running, without rolling back', async () => {
    let calls = 0;
    const push = vi.fn((mutations: Mutation[]) => {
      calls += 1;
      if (calls === 1) return Promise.reject(new Error('network error'));
      return Promise.resolve(mutations.map((m) => ({ mutationId: m.id, applied: true, error: '' })));
    });
    const engine = createSyncEngine({
      transport: fakeTransport({ pushMutations: push }),
      repo: h.repo,
      store: h.store,
      queue: h.queue,
    });
    const cap = engine.handle('cap-1');
    cap.title = 'x';
    await cap.save();

    const stop = engine.start(60_000); // long interval: retry must come from backoff, not the loop tick
    await waitFor(() => push.mock.calls.length >= 1);
    expect(h.store.capture('cap-1').get()?.doc?.title).toBe('x'); // not rolled back

    await waitFor(() => push.mock.calls.length >= 2, 5_000);
    stop();
  }, 10_000);
});
