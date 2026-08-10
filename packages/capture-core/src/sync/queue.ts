import type { IDBPDatabase } from 'idb';
import type { CaptureDbSchema } from '../storage/schema.js';
import type { FieldKey, FieldValue, Mutation } from './mutations.js';

export const QUEUE_MUTATION_CAP = 5_000;
export const QUEUE_BYTE_CAP = 5 * 1024 * 1024;
/** The "pending sync" indicator lights up once the queue is this full. */
export const QUEUE_WARN_RATIO = 0.9;

/**
 * A mutation as held in the local queue: the wire `Mutation` plus enough to
 * roll a rejected edit back to its pre-mutation state and replay whatever
 * landed on top of it (see engine.ts `reconcileReject`).
 */
export type QueuedMutation = {
  mutation: Mutation;
  field: FieldKey;
  base: FieldValue;
};

export type QueueCapacity = {
  count: number;
  bytes: number;
  atCap: boolean;
  nearCap: boolean;
};

export type MutationQueue = {
  /** Current depth, for the "pending sync" indicator. */
  capacity(): Promise<QueueCapacity>;
  /** True if `nextCount` more mutations totalling `nextBytes` still fit under both caps. */
  hasRoom(nextCount: number, nextBytes: number): Promise<boolean>;
  enqueue(item: QueuedMutation): Promise<void>;
  /** All queued mutations in push order (mutation ULIDs sort lexically). */
  list(): Promise<QueuedMutation[]>;
  listForField(captureId: string, field: FieldKey): Promise<QueuedMutation[]>;
  remove(mutationId: string): Promise<void>;
};

export function estimateBytes(mutation: Mutation): number {
  return new TextEncoder().encode(JSON.stringify(mutation)).byteLength;
}

export function createMutationQueue(db: IDBPDatabase<CaptureDbSchema>): MutationQueue {
  async function all(): Promise<QueuedMutation[]> {
    // IndexedDB's `getAll()` is spec-guaranteed to return records in ascending
    // primary-key order — the store's key is `mutation.id` (a ULID), so this
    // is already push order with no re-sort needed.
    return db.getAll('sync_mutations');
  }

  async function totals(): Promise<{ count: number; bytes: number }> {
    const rows = await all();
    return {
      count: rows.length,
      bytes: rows.reduce((sum, row) => sum + estimateBytes(row.mutation), 0),
    };
  }

  return {
    async capacity(): Promise<QueueCapacity> {
      const { count, bytes } = await totals();
      return {
        count,
        bytes,
        atCap: count >= QUEUE_MUTATION_CAP || bytes >= QUEUE_BYTE_CAP,
        nearCap:
          count >= QUEUE_MUTATION_CAP * QUEUE_WARN_RATIO ||
          bytes >= QUEUE_BYTE_CAP * QUEUE_WARN_RATIO,
      };
    },

    async hasRoom(nextCount: number, nextBytes: number): Promise<boolean> {
      const { count, bytes } = await totals();
      return count + nextCount <= QUEUE_MUTATION_CAP && bytes + nextBytes <= QUEUE_BYTE_CAP;
    },

    async enqueue(item: QueuedMutation): Promise<void> {
      await db.put('sync_mutations', item);
    },

    async list(): Promise<QueuedMutation[]> {
      return all();
    },

    async listForField(captureId: string, field: FieldKey): Promise<QueuedMutation[]> {
      const rows = await all();
      return rows.filter((row) => row.mutation.captureId === captureId && row.field === field);
    },

    async remove(mutationId: string): Promise<void> {
      await db.delete('sync_mutations', mutationId);
    },
  };
}
