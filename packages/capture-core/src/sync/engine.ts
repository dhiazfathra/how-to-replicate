import { newId } from '../identity.js';
import type { Capture } from '../types/capture.js';
import type { CaptureRepository } from '../storage/repository.js';
import type { CaptureStore } from '../store/capture-store.js';
import {
  applyMutation,
  fieldForOp,
  getFieldValue,
  topLevelKeyOf,
  withFieldValue,
  type FieldKey,
  type MutationOp,
} from './mutations.js';
import { type MutationQueue, type QueuedMutation, estimateBytes } from './queue.js';
import { hasTransport, type SyncTransport } from './transport.js';

export const BASE_BACKOFF_MS = 500;
export const MAX_BACKOFF_MS = 30_000;

/** Exponential backoff with full jitter. `attempt` is 1-based. */
export function computeBackoffMs(attempt: number, random: () => number = Math.random): number {
  const cap = Math.min(MAX_BACKOFF_MS, BASE_BACKOFF_MS * 2 ** Math.max(0, attempt - 1));
  return Math.round(random() * cap);
}

export type SaveResult = { ok: true } | { ok: false; reason: 'queue-full' };

export type CaptureHandle = {
  title: string;
  summary: string;
  tags: string[];
  assigneeUserId: string;
  /** Writes the local observable synchronously, then enqueues — the UI contract. */
  save(): Promise<SaveResult>;
  appendComment(body: string): Promise<SaveResult>;
};

export type SyncEngineConfig = {
  transport: SyncTransport | undefined;
  repo: CaptureRepository;
  store: CaptureStore;
  queue: MutationQueue;
  /** Sync is scoped to one workspace per engine instance. No-op without it. */
  workspaceId?: string;
  now?: () => number;
};

export type SyncEngine = {
  /** False when no transport is configured — a Phase 0 build stays inert. */
  isActive(): boolean;
  handle(captureId: string): CaptureHandle;
  /** Push every queued mutation once. No-op if inert or already flushing. */
  flush(): Promise<void>;
  /** Pull every available delta page once, advancing the persisted cursor. */
  pull(): Promise<void>;
  /** Runs flush+pull on `intervalMs`, starting immediately. Returns a stop function. */
  start(intervalMs?: number): () => void;
};

function requireCapture(store: CaptureStore, id: string): Capture {
  const current = store.capture(id).get();
  if (!current) throw new Error(`sync: unknown capture "${id}"`);
  return current;
}

/**
 * Recompute a field's local value after a mutation targeting it is rejected.
 * Rolls back to the rejected mutation's pre-mutation `base`, then replays
 * every mutation still queued for that field with a strictly newer ULID —
 * i.e. edits the user made while the reject was in flight — so a late,
 * out-of-order reject for an old edit never clobbers a newer one.
 */
async function reconcileReject(
  store: CaptureStore,
  queue: MutationQueue,
  rejected: QueuedMutation,
): Promise<void> {
  const captureId = rejected.mutation.captureId;
  const current = requireCapture(store, captureId);

  // `listForField` already returns push-order (ascending mutation ULID), and
  // filtering preserves that order — no re-sort needed.
  const newerPending = (await queue.listForField(captureId, rejected.field)).filter(
    (item) => item.mutation.id > rejected.mutation.id,
  );

  // Restore the rejected mutation's pre-mutation value directly (no synthetic
  // MutationOp needed — `withFieldValue` sets any field, including the
  // append-only `metadata.comments`, as a plain replace), then replay every
  // mutation still queued for the field on top of it.
  let reconstructed: Capture = withFieldValue(current, rejected.field, rejected.base);
  for (const item of newerPending) {
    reconstructed = applyMutation(reconstructed, item.mutation.op);
  }

  const topKey = topLevelKeyOf(rejected.field);
  await store.setField(captureId, topKey, reconstructed[topKey]);
}

export function createSyncEngine(config: SyncEngineConfig): SyncEngine {
  const { transport, repo, store, queue, workspaceId, now = () => Date.now() } = config;

  let flushing = false;
  let attempt = 0;
  let pushRetryTimer: ReturnType<typeof setTimeout> | undefined;
  let loopTimer: ReturnType<typeof setTimeout> | undefined;
  let stopped = true;

  async function commit(
    captureId: string,
    ops: { field: FieldKey; op: MutationOp }[],
  ): Promise<SaveResult> {
    if (ops.length === 0) return { ok: true };

    const items: QueuedMutation[] = [];
    let working = requireCapture(store, captureId);
    // clientT is an offset from this capture's epoch, per Mutation.clientT's
    // contract and the project convention (no wall-clock timestamps in a
    // capture) — never `now()` directly.
    const clientT = now() - working.epoch;
    for (const { field, op } of ops) {
      const base = getFieldValue(working, field);
      items.push({ mutation: { id: newId(), captureId, op, clientT }, field, base });
      working = applyMutation(working, op);
    }

    const totalBytes = items.reduce((sum, item) => sum + estimateBytes(item.mutation), 0);
    if (!(await queue.hasRoom(items.length, totalBytes))) {
      return { ok: false, reason: 'queue-full' };
    }

    for (const key of new Set(ops.map(({ field }) => topLevelKeyOf(field)))) {
      await store.setField(captureId, key, working[key]);
    }
    for (const item of items) {
      await queue.enqueue(item);
    }
    return { ok: true };
  }

  function handle(captureId: string): CaptureHandle {
    const snapshot = requireCapture(store, captureId);
    let title = snapshot.doc?.title ?? '';
    let summary = snapshot.doc?.summary ?? '';
    let tags = Array.isArray(snapshot.metadata.tags) ? [...(snapshot.metadata.tags as string[])] : [];
    let assigneeUserId =
      typeof snapshot.metadata.assigneeUserId === 'string' ? snapshot.metadata.assigneeUserId : '';

    return {
      get title() {
        return title;
      },
      set title(v: string) {
        title = v;
      },
      get summary() {
        return summary;
      },
      set summary(v: string) {
        summary = v;
      },
      get tags() {
        return tags;
      },
      set tags(v: string[]) {
        tags = v;
      },
      get assigneeUserId() {
        return assigneeUserId;
      },
      set assigneeUserId(v: string) {
        assigneeUserId = v;
      },

      async save(): Promise<SaveResult> {
        const live = requireCapture(store, captureId);
        const ops: { field: FieldKey; op: MutationOp }[] = [];
        if (title !== (live.doc?.title ?? '')) ops.push({ field: 'doc.title', op: { type: 'setTitle', title } });
        if (summary !== (live.doc?.summary ?? ''))
          ops.push({ field: 'doc.summary', op: { type: 'setSummary', summary } });
        const liveTags = Array.isArray(live.metadata.tags) ? (live.metadata.tags as string[]) : [];
        if (tags.length !== liveTags.length || tags.some((t, i) => t !== liveTags[i]))
          ops.push({ field: 'metadata.tags', op: { type: 'setTags', tags } });
        const liveAssignee =
          typeof live.metadata.assigneeUserId === 'string' ? live.metadata.assigneeUserId : '';
        if (assigneeUserId !== liveAssignee)
          ops.push({ field: 'metadata.assigneeUserId', op: { type: 'assign', assigneeUserId } });

        const result = await commit(captureId, ops);
        if (!result.ok) {
          // Refused: fall back to whatever is currently persisted, so a
          // retry of `save()` diffs against reality, not the refused draft.
          const live2 = requireCapture(store, captureId);
          title = live2.doc?.title ?? '';
          summary = live2.doc?.summary ?? '';
          tags = Array.isArray(live2.metadata.tags) ? [...(live2.metadata.tags as string[])] : [];
          assigneeUserId =
            typeof live2.metadata.assigneeUserId === 'string' ? live2.metadata.assigneeUserId : '';
        }
        return result;
      },

      async appendComment(body: string): Promise<SaveResult> {
        return commit(captureId, [
          {
            field: 'metadata.comments',
            op: { type: 'appendComment', commentId: newId(), body },
          },
        ]);
      },
    };
  }

  async function flush(): Promise<void> {
    if (!hasTransport(transport) || flushing) return;
    flushing = true;
    try {
      const items = await queue.list();
      if (items.length === 0) return;

      let results;
      try {
        results = await transport.pushMutations(items.map((item) => item.mutation));
      } catch {
        attempt += 1;
        if (!stopped) {
          clearTimeout(pushRetryTimer);
          pushRetryTimer = setTimeout(() => void flush(), computeBackoffMs(attempt));
        }
        return;
      }

      attempt = 0;
      const byId = new Map(items.map((item) => [item.mutation.id, item]));
      for (const result of results) {
        const item = byId.get(result.mutationId);
        if (!item) continue;
        if (!result.applied) {
          await reconcileReject(store, queue, item);
        }
        await queue.remove(result.mutationId);
      }
    } finally {
      flushing = false;
    }
  }

  async function pull(): Promise<void> {
    if (!hasTransport(transport) || !workspaceId) return;

    let since = await repo.getSyncCursor(workspaceId);
    for (;;) {
      let page;
      try {
        page = await transport.pullDeltas(workspaceId, since);
      } catch {
        return;
      }

      for (const mutation of page.mutations) {
        const current = store.capture(mutation.captureId).get();
        if (!current) continue;
        const field = fieldForOp(mutation.op);
        const updated = applyMutation(current, mutation.op);
        await store.setField(mutation.captureId, topLevelKeyOf(field), updated[topLevelKeyOf(field)]);
      }

      since = page.revision;
      await repo.putSyncCursor(workspaceId, since);
      if (!page.hasMore) break;
    }
  }

  function start(intervalMs = 15_000): () => void {
    stopped = false;
    // `stop()` both flips `stopped` and clears the pending timer synchronously,
    // so a scheduled `loop()` never fires after stop — no re-check needed here.
    const loop = (): void => {
      void flush()
        .then(() => pull())
        .finally(() => {
          if (!stopped) loopTimer = setTimeout(loop, intervalMs);
        });
    };
    loop();
    return () => {
      stopped = true;
      clearTimeout(loopTimer);
      clearTimeout(pushRetryTimer);
    };
  }

  return {
    isActive: () => hasTransport(transport),
    handle,
    flush,
    pull,
    start,
  };
}
