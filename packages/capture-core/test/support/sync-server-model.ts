import { fieldForOp, type FieldKey, type Mutation } from '../../src/sync/mutations.js';
import type { PullDeltasResult, PushMutationsResult, SyncTransport } from '../../src/sync/transport.js';

/**
 * Mirrors the LWW tuple `services/sync-gateway/internal/gateway/store.go`
 * `FieldVersion` records per (capture, field): server-received timestamp,
 * the capture's revision at acceptance time, and the mutation's own ULID.
 */
export type FieldVersion = { serverT: number; revision: number; mutationId: string };

/**
 * Faithful port of `gateway.go`'s `wins()`: candidate beats existing by
 * server timestamp, then revision, then mutation ID, in that order. Every
 * branch mirrors the Go source line for line.
 */
export function wins(candidate: FieldVersion, existing: FieldVersion): boolean {
  if (candidate.serverT !== existing.serverT) return candidate.serverT > existing.serverT;
  if (candidate.revision !== existing.revision) return candidate.revision > existing.revision;
  return candidate.mutationId > existing.mutationId;
}

type DeltaRow = { seq: number; mutation: Mutation };

type CaptureState = {
  revision: number;
  fieldVersions: Map<FieldKey, FieldVersion>;
  deltaLog: DeltaRow[];
};

/**
 * A faithful in-process model of sync-gateway's mutation-intake logic
 * (`gateway.go` `applyOne`/`PushMutations`/`PullDeltas`) — not the whole Go
 * service (no HTTP, no Postgres), just the tuple-comparison, idempotency,
 * and delta-log semantics a client must interoperate with correctly.
 *
 * Faithfulness notes, each traceable to gateway.go:
 * - Idempotent by mutation ULID (`InsertMutationIfNew`): redelivering a
 *   mutation id returns the exact same `PushMutationsResult` without
 *   re-racing it against current state.
 * - `serverT` advances once per "tick" (one call to `tick()`), the
 *   in-process analogue of `g.Now()` — every mutation accepted within the
 *   same tick shares a timestamp, which is what makes the revision and
 *   mutation-ID tie-break levels reachable at all (two mutations landing in
 *   the same wall-clock instant is the normal case at real request
 *   granularity, not an edge case).
 * - Every *newly accepted* mutation (winner or not) advances the capture's
 *   revision and is appended to the delta log in acceptance order — exactly
 *   `UpdateCaptureRevisionAndDoc` + `PullMutationsSince`, which record and
 *   replay superseded writes too, not just winners.
 * - `appendComment` never conflicts and always lands in the delta log
 *   (mirrors `isAppend` short-circuiting before the `wins()` check).
 */
export function createSyncServerModel(): SyncTransport & { tick(): void } {
  const captures = new Map<string, CaptureState>();
  const appliedMutationIds = new Map<string, PushMutationsResult>();
  let serverT = 0;

  function stateFor(captureId: string): CaptureState {
    let state = captures.get(captureId);
    if (!state) {
      state = { revision: 0, fieldVersions: new Map(), deltaLog: [] };
      captures.set(captureId, state);
    }
    return state;
  }

  function applyOne(mutation: Mutation): PushMutationsResult {
    const replay = appliedMutationIds.get(mutation.id);
    if (replay) return replay;

    const state = stateFor(mutation.captureId);
    state.revision += 1;
    const seq = state.revision;
    state.deltaLog.push({ seq, mutation });

    const field = fieldForOp(mutation.op);
    if (field !== 'metadata.comments') {
      const candidate: FieldVersion = { serverT, revision: seq, mutationId: mutation.id };
      const existing = state.fieldVersions.get(field);
      if (!existing || wins(candidate, existing)) {
        state.fieldVersions.set(field, candidate);
      }
    }

    const result: PushMutationsResult = { mutationId: mutation.id, applied: true, error: '' };
    appliedMutationIds.set(mutation.id, result);
    return result;
  }

  return {
    /** Advance the shared server clock — call between rounds of concurrent pushes. */
    tick(): void {
      serverT += 1;
    },

    pushMutations(mutations: Mutation[]): Promise<PushMutationsResult[]> {
      return Promise.resolve(mutations.map(applyOne));
    },

    // Real `pullDeltas` is keyed by workspaceId and can span many captures;
    // this model only ever backs a single shared capture per test, so the
    // harness passes that capture's id as `workspaceId` too and this stays a
    // 1:1 lookup — see test/sync-convergence.property.test.ts's harness.
    pullDeltas(workspaceId: string, since: number): Promise<PullDeltasResult> {
      const state = stateFor(workspaceId);
      const rows = state.deltaLog.filter((row) => row.seq > since);
      const revision = rows.length > 0 ? rows[rows.length - 1]!.seq : since;
      return Promise.resolve({
        mutations: rows.map((row) => row.mutation),
        revision,
        hasMore: false,
        // Manifest completeness is out of scope for convergence testing
        // (see requestAssetUpload/completeAssetUpload below) — no captures.
        captures: [],
      });
    },

    requestAssetUpload(): Promise<never> {
      return Promise.reject(new Error('sync-server-model: assets are out of scope for convergence testing'));
    },
    completeAssetUpload(): Promise<never> {
      return Promise.reject(new Error('sync-server-model: assets are out of scope for convergence testing'));
    },
  };
}
