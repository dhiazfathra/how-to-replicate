import { openDB, type DBSchema, type IDBPDatabase } from 'idb';
import { openCaptureDb, DEFAULT_DB_NAME } from '../storage/db.js';
import type { CaptureDbSchema } from '../storage/schema.js';
import { CaptureRepository } from '../storage/repository.js';
import { createCaptureStore, type CaptureStore } from './capture-store.js';

/** The (subject, workspaceId) pair that identifies a local storage partition. */
export type Identity = { subject: string; workspaceId: string };

export type IdentityPartitionOptions = {
  /** Injectable so tests can point at fake-indexeddb instead of a real browser. */
  indexedDB?: IDBFactory;
};

// Fixed, never-partitioned database: its whole job is saying which partition
// is current, so it can never itself live inside a partition.
const POINTER_DB_NAME = 'htr-identity-pointer';
const POINTER_STORE = 'pointer';
const POINTER_KEY = 'current';

function partitionDbName(identity: Identity): string {
  return `${DEFAULT_DB_NAME}::${identity.subject}::${identity.workspaceId}`;
}

function sameIdentity(a: Identity | null, b: Identity | null): boolean {
  if (a === null || b === null) return a === b;
  return a.subject === b.subject && a.workspaceId === b.workspaceId;
}

interface PointerDbSchema extends DBSchema {
  [POINTER_STORE]: {
    key: string;
    value: Identity;
  };
}

function openPointerDb(
  options: IdentityPartitionOptions,
): Promise<IDBPDatabase<PointerDbSchema>> {
  return openDB<PointerDbSchema>(POINTER_DB_NAME, 1, {
    upgrade(db) {
      db.createObjectStore(POINTER_STORE);
    },
    ...(options.indexedDB ? { indexedDB: options.indexedDB } : {}),
  });
}

export async function getRecordedIdentity(
  options: IdentityPartitionOptions = {},
): Promise<Identity | null> {
  const db = await openPointerDb(options);
  try {
    const value = await db.get(POINTER_STORE, POINTER_KEY);
    return value ?? null;
  } finally {
    db.close();
  }
}

export async function recordIdentity(
  identity: Identity | null,
  options: IdentityPartitionOptions = {},
): Promise<void> {
  const db = await openPointerDb(options);
  try {
    if (identity === null) {
      await db.delete(POINTER_STORE, POINTER_KEY);
    } else {
      await db.put(POINTER_STORE, identity, POINTER_KEY);
    }
  } finally {
    db.close();
  }
}

// ponytail: single-tab, single-active-partition assumption — one module-level
// handle to the currently open partition db, so purge can close it before
// deleting. Upgrade to a registry if this app ever opens multiple partitions
// concurrently (e.g. multi-window sync of the same identity).
let openPartitionHandle: IDBPDatabase<CaptureDbSchema> | undefined;

function deleteDatabase(name: string, options: IdentityPartitionOptions): Promise<void> {
  return new Promise((resolve, reject) => {
    const factory = options.indexedDB ?? indexedDB;
    const request = factory.deleteDatabase(name);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error ?? new Error(`deleteDatabase(${name}) failed`));
    // onblocked fires if some other connection to this db is still open; we
    // close our own handle before ever calling this, so a block here means an
    // external caller holds a stale handle. Don't resolve early — wait for
    // the eventual success/error the spec guarantees once it unblocks.
  });
}

/**
 * Purge a prior identity's partition: close our own connection to it (if
 * open) so the delete doesn't hang blocked on ourselves, then delete the
 * database and await completion.
 */
export async function purgePartition(
  identity: Identity,
  options: IdentityPartitionOptions = {},
): Promise<void> {
  if (openPartitionHandle) {
    openPartitionHandle.close();
    openPartitionHandle = undefined;
  }
  await deleteDatabase(partitionDbName(identity), options);
}

/**
 * The single gate through which an identity change may pass: compares
 * `newIdentity` against the recorded one, and if they differ (including
 * `newIdentity: null` on logout), purges the old partition and only then
 * updates the pointer. A no-op when identities match (token refresh, no
 * identity change) — never touches `deleteDatabase` in that case.
 */
export async function ensureIdentityPartition(
  newIdentity: Identity | null,
  options: IdentityPartitionOptions = {},
): Promise<void> {
  const recorded = await getRecordedIdentity(options);
  if (sameIdentity(recorded, newIdentity)) return;
  if (recorded) {
    await purgePartition(recorded, options);
  }
  await recordIdentity(newIdentity, options);
}

/**
 * Awaits `ensureIdentityPartition` (purge-before-render, local-pointer-only,
 * no network) then opens the new partition's database. This is the only
 * function that opens a partition db, so nothing can ever read a prior
 * identity's data via this path. Callers that also need a `CaptureRepository`
 * (e.g. to read events/assets, not just the observable store) should use
 * this directly instead of `openIdentityPartition`.
 */
export async function openIdentityPartitionDb(
  identity: Identity,
  options: IdentityPartitionOptions = {},
): Promise<IDBPDatabase<CaptureDbSchema>> {
  await ensureIdentityPartition(identity, options);
  const db = await openCaptureDb(partitionDbName(identity), options);
  openPartitionHandle = db;
  return db;
}

/**
 * The only entrypoint that opens a partition's store. See
 * `openIdentityPartitionDb` for the purge-before-open guarantee.
 */
export async function openIdentityPartition(
  identity: Identity,
  options: IdentityPartitionOptions = {},
): Promise<CaptureStore> {
  const db = await openIdentityPartitionDb(identity, options);
  const store = createCaptureStore(new CaptureRepository(db));
  await store.hydrate();
  return store;
}
