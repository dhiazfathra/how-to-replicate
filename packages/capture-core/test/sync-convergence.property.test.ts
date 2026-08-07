import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { IDBFactory } from 'fake-indexeddb';
import { openCaptureDb } from '../src/storage/db.js';
import { CaptureRepository } from '../src/storage/repository.js';
import { createCaptureStore, type CaptureStore } from '../src/store/capture-store.js';
import { createMutationQueue, type MutationQueue } from '../src/sync/queue.js';
import { createSyncEngine, type SyncEngine } from '../src/sync/engine.js';
import type { Capture } from '../src/types/capture.js';
import type { SyncTransport } from '../src/sync/transport.js';
import { createSyncServerModel, wins, type FieldVersion } from './support/sync-server-model.js';

// Spec §18 names this test specifically: it proves the last-write-wins
// design (Tasks 4-7) actually converges under adversarial interleavings. Per
// CLAUDE.md and the task brief, this property must never be weakened, have
// its iteration count cut, or be marked flaky — a failure here means the
// sync rules (client or server) are wrong, not that the test is.

const CAPTURE_ID = 'shared-cap';
// The server model is keyed by "workspaceId" for pullDeltas and by
// mutation.captureId for pushMutations; using the same string for both, and
// a single shared capture across every client, keeps the model's lookup 1:1
// (see sync-server-model.ts).
const WORKSPACE_ID = CAPTURE_ID;

function makeCapture(): Capture {
  return {
    id: CAPTURE_ID,
    workspaceId: WORKSPACE_ID,
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

type FinalState = {
  title: string;
  summary: string;
  tags: string[];
  assigneeUserId: string;
  comments: { id: string; body: string }[];
};

function finalStateOf(capture: Capture | undefined): FinalState {
  return {
    title: capture?.doc?.title ?? '',
    summary: capture?.doc?.summary ?? '',
    tags: Array.isArray(capture?.metadata.tags) ? (capture.metadata.tags as string[]) : [],
    assigneeUserId:
      typeof capture?.metadata.assigneeUserId === 'string' ? capture.metadata.assigneeUserId : '',
    comments: Array.isArray(capture?.metadata.comments)
      ? (capture.metadata.comments as { id: string; body: string }[])
      : [],
  };
}

type ClientHarness = { engine: SyncEngine; store: CaptureStore; queue: MutationQueue };

async function makeClient(server: SyncTransport): Promise<ClientHarness> {
  const factory = new IDBFactory();
  const db = await openCaptureDb(`convergence-${Math.random()}`, { indexedDB: factory });
  const repo = new CaptureRepository(db);
  const store = createCaptureStore(repo);
  const queue = createMutationQueue(db);
  store.putLocal(makeCapture());
  await repo.putCapture(makeCapture());
  const engine = createSyncEngine({ transport: server, repo, store, queue, workspaceId: WORKSPACE_ID });
  return { engine, store, queue };
}

// --- the closed op set the generator covers, mirrored from mutations.ts ---
type EditSpec =
  | { type: 'setTitle'; title: string }
  | { type: 'setSummary'; summary: string }
  | { type: 'setTags'; tags: string[] }
  | { type: 'assign'; assigneeUserId: string }
  | { type: 'appendComment'; body: string };

type Step =
  | { kind: 'edit'; client: number; edit: EditSpec }
  | { kind: 'flush'; client: number; duplicate: boolean }
  | { kind: 'pull'; client: number };

async function applyEdit(engine: SyncEngine, edit: EditSpec): Promise<void> {
  const handle = engine.handle(CAPTURE_ID);
  switch (edit.type) {
    case 'setTitle':
      handle.title = edit.title;
      await handle.save();
      return;
    case 'setSummary':
      handle.summary = edit.summary;
      await handle.save();
      return;
    case 'setTags':
      handle.tags = edit.tags;
      await handle.save();
      return;
    case 'assign':
      handle.assigneeUserId = edit.assigneeUserId;
      await handle.save();
      return;
    case 'appendComment':
      await handle.appendComment(edit.body);
      return;
  }
}

const shortString = fc.string({ maxLength: 8 });

const editArb: fc.Arbitrary<EditSpec> = fc.oneof(
  fc.record({ type: fc.constant('setTitle' as const), title: shortString }),
  fc.record({ type: fc.constant('setSummary' as const), summary: shortString }),
  fc.record({ type: fc.constant('setTags' as const), tags: fc.array(shortString, { maxLength: 3 }) }),
  fc.record({ type: fc.constant('assign' as const), assigneeUserId: shortString }),
  fc.record({ type: fc.constant('appendComment' as const), body: shortString }),
);

const CLIENT_COUNT = 3;
const clientIndexArb = fc.integer({ min: 0, max: CLIENT_COUNT - 1 });

const stepArb: fc.Arbitrary<Step> = fc.oneof(
  { arbitrary: fc.record({ kind: fc.constant('edit' as const), client: clientIndexArb, edit: editArb }), weight: 3 },
  {
    arbitrary: fc.record({
      kind: fc.constant('flush' as const),
      client: clientIndexArb,
      duplicate: fc.boolean(),
    }),
    weight: 2,
  },
  { arbitrary: fc.record({ kind: fc.constant('pull' as const), client: clientIndexArb }), weight: 2 },
);

// Enough steps and iterations to genuinely stress same-field/different-field
// concurrent writes, duplicate delivery, and out-of-order delivery (a step
// sequence's order *is* delivery order here — see runPlan) across shrinking.
const planArb = fc.array(stepArb, { minLength: 1, maxLength: 30 });

async function runPlan(plan: Step[]): Promise<FinalState[]> {
  const server = createSyncServerModel();
  const clients = await Promise.all(Array.from({ length: CLIENT_COUNT }, () => makeClient(server)));

  for (const step of plan) {
    const { engine, queue } = clients[step.client]!;
    switch (step.kind) {
      case 'edit':
        await applyEdit(engine, step.edit);
        break;
      case 'flush': {
        // Snapshot before flushing: `engine.flush()` dequeues on success, so
        // this is the only point a genuine network-level retransmission can
        // be modeled — resending the exact same mutation ULIDs the engine
        // just pushed, which the server model must treat as a no-op.
        const pending = await queue.list();
        await engine.flush();
        if (step.duplicate && pending.length > 0) {
          await server.pushMutations(pending.map((item) => item.mutation));
        }
        break;
      }
      case 'pull':
        await engine.pull();
        break;
    }
    // Every accepted push shares a server timestamp with the rest of its
    // step, but the *next* step (possibly a different client's push)
    // observes a later one — this is what makes same-tick ties (same
    // "request") and cross-tick ordering (later requests winning) both
    // exercised across a plan, matching gateway.go's one-`Now()`-per-request
    // shape.
    server.tick();
  }

  // Drain to quiescence: every client flushes whatever it queued and pulls
  // every delta so far, repeated until nothing new can appear. Bounded by
  // plan length + client count — more than enough rounds for any client to
  // see every mutation that plan could ever have produced.
  for (let round = 0; round < plan.length + CLIENT_COUNT + 1; round += 1) {
    for (const { engine } of clients) {
      await engine.flush();
      await engine.pull();
    }
  }

  return clients.map(({ store }) => finalStateOf(store.capture(CAPTURE_ID).get()));
}

describe('sync-server-model wins() — faithful port of gateway.go wins()', () => {
  // Direct unit coverage of every tie-break branch, mirrored from
  // services/sync-gateway/internal/gateway/gateway_test.go
  // TestPushMutations_ConflictResolution. The mutation-ID tie-break level is
  // not reachable from real capture traffic generated by the property below
  // (revisions are unique per capture by construction, so a revision tie
  // never occurs) but the branch exists in the ported function and must be
  // exercised directly, exactly as gateway.go's own unit test does.
  it('later timestamp wins outright, even with a lower revision', () => {
    const existing: FieldVersion = { serverT: 1, revision: 5, mutationId: 'z' };
    expect(wins({ serverT: 2, revision: 1, mutationId: 'a' }, existing)).toBe(true);
    expect(wins({ serverT: 0, revision: 9, mutationId: 'z' }, existing)).toBe(false);
  });

  it('equal timestamp: higher revision breaks the tie', () => {
    const existing: FieldVersion = { serverT: 1, revision: 3, mutationId: 'm' };
    expect(wins({ serverT: 1, revision: 4, mutationId: 'a' }, existing)).toBe(true);
    expect(wins({ serverT: 1, revision: 2, mutationId: 'z' }, existing)).toBe(false);
  });

  it('equal timestamp and revision: larger mutation ID wins', () => {
    const existing: FieldVersion = { serverT: 1, revision: 3, mutationId: 'aaa' };
    expect(wins({ serverT: 1, revision: 3, mutationId: 'bbb' }, existing)).toBe(true);
    expect(wins({ serverT: 1, revision: 3, mutationId: 'aaa' }, existing)).toBe(false);
  });
});

describe('sync convergence property', () => {
  it(
    'every client converges to one identical state under random interleavings, delays, and duplicates',
    { timeout: 60_000 },
    async () => {
      // fast-check's default reporter already prints a shrunk minimal
      // counterexample (the `plan` array) on failure — that *is* "a minimal
      // reproducing interleaving, not just 'property violated'": each Step
      // names its client index, op, and (for flush) whether it duplicated
      // delivery, so a failing run's printed `plan` is directly replayable.
      await fc.assert(
        fc.asyncProperty(planArb, async (plan) => {
          const states = await runPlan(plan);
          const [first, ...rest] = states;
          for (const other of rest) {
            expect(other).toEqual(first);
          }
        }),
        { numRuns: 150 },
      );
    },
  );
});
