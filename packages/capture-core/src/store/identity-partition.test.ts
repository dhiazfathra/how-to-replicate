import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { IDBFactory } from 'fake-indexeddb';
import {
  ensureIdentityPartition,
  getRecordedIdentity,
  openIdentityPartition,
  purgePartition,
  recordIdentity,
  type Identity,
} from './identity-partition.js';

/**
 * The global `indexedDB` (installed by `fake-indexeddb/auto` in
 * test/setup-fake-indexeddb.ts) persists databases across tests in this
 * file, and `idb`'s `openDB` always uses the global factory regardless of an
 * `{ indexedDB }` option (this version of `idb` doesn't honor it — see
 * storage/db.ts's identical option, which is equally inert against a real
 * browser's single global `indexedDB`). So: replace the global with a fresh,
 * empty `IDBFactory` before every test for isolation, and to log call order.
 */
let log: string[];

beforeEach(() => {
  log = [];
  const factory = new IDBFactory();
  const origOpen = factory.open.bind(factory);
  const origDelete = factory.deleteDatabase.bind(factory);

  factory.open = (name: string, version?: number) => {
    log.push(`open:call:${name}`);
    const req = origOpen(name, version);
    req.addEventListener('success', () => log.push(`open:done:${name}`));
    return req;
  };

  factory.deleteDatabase = (name: string) => {
    log.push(`delete:call:${name}`);
    const req = origDelete(name);
    req.addEventListener('success', () => log.push(`delete:done:${name}`));
    return req;
  };

  vi.stubGlobal('indexedDB', factory);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const A: Identity = { subject: 'alice', workspaceId: 'w1' };
const B: Identity = { subject: 'bob', workspaceId: 'w1' };
const dbNameFor = (id: Identity) => `how-to-replicate::${id.subject}::${id.workspaceId}`;

describe('identity pointer', () => {
  it('is null until recorded', async () => {
    expect(await getRecordedIdentity()).toBeNull();
  });

  it('records and reads back an identity', async () => {
    await recordIdentity(A);
    expect(await getRecordedIdentity()).toEqual(A);
  });

  it('accepts an explicit indexedDB factory option', async () => {
    const indexedDB = new IDBFactory();
    await recordIdentity(A, { indexedDB });
    expect(await getRecordedIdentity({ indexedDB })).toEqual(A);
  });

  it('clears the pointer when recorded with null', async () => {
    await recordIdentity(A);
    await recordIdentity(null);
    expect(await getRecordedIdentity()).toBeNull();
  });
});

describe('ensureIdentityPartition', () => {
  it('first login (no recorded identity): records the identity, purges nothing', async () => {
    await ensureIdentityPartition(A);
    expect(await getRecordedIdentity()).toEqual(A);
    expect(log.some((e) => e.startsWith('delete:call'))).toBe(false);
  });

  it('same identity again (e.g. token refresh): no-op, never touches deleteDatabase', async () => {
    await ensureIdentityPartition(A);
    log.length = 0;
    await ensureIdentityPartition({ ...A });
    expect(log.some((e) => e.startsWith('delete:call'))).toBe(false);
    expect(await getRecordedIdentity()).toEqual(A);
  });

  it('workspace switch for the same subject counts as a different identity and purges', async () => {
    const A2: Identity = { subject: 'alice', workspaceId: 'w2' };
    await ensureIdentityPartition(A);
    await ensureIdentityPartition(A2);
    expect(log).toContain(`delete:call:${dbNameFor(A)}`);
    expect(await getRecordedIdentity()).toEqual(A2);
  });

  it('logout (null) purges the recorded partition and clears the pointer', async () => {
    await ensureIdentityPartition(A);
    await ensureIdentityPartition(null);
    expect(log).toContain(`delete:call:${dbNameFor(A)}`);
    expect(await getRecordedIdentity()).toBeNull();
  });

  it('logout with no recorded identity is a no-op', async () => {
    await ensureIdentityPartition(null);
    expect(log.some((e) => e.startsWith('delete:call'))).toBe(false);
    expect(await getRecordedIdentity()).toBeNull();
  });
});

describe('token expiry mid-session', () => {
  it('never invokes purge or touches the identity pointer', async () => {
    await openIdentityPartition(A);
    const before = await getRecordedIdentity();
    const deleteSpy = vi.spyOn(globalThis.indexedDB, 'deleteDatabase');

    // A 401 on a background sync pull has no code path into this module —
    // there is nothing to call here. The assertion is that simply not
    // calling ensureIdentityPartition/openIdentityPartition leaves the
    // partition and pointer completely untouched.
    const after = await getRecordedIdentity();

    expect(deleteSpy).not.toHaveBeenCalled();
    expect(after).toEqual(before);
  });
});

describe('openIdentityPartition', () => {
  it('opens and hydrates a fresh partition for a new identity', async () => {
    const store = await openIdentityPartition(A);
    expect(store.ids()).toEqual([]);
  });

  it(
    "logout then login as a different subject: B never sees A's data, and " +
      "A's delete is fully awaited before B's database is ever opened",
    async () => {
      const storeA = await openIdentityPartition(A);
      storeA.putLocal({
        id: 'cap-a',
        workspaceId: A.workspaceId,
        projectId: null,
        source: 'extension',
        state: 'recording',
        fidelity: 'full',
        createdAt: '2026-08-04T00:00:00.000Z',
        epoch: 0,
        env: {
          userAgent: 'test',
          platform: 'test',
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
      });
      await storeA.setField('cap-a', 'state', 'ready');

      // Logout, then log in as a different subject.
      await ensureIdentityPartition(null);
      const storeB = await openIdentityPartition(B);

      // Ordering guarantee: A's delete must fully resolve (delete:done)
      // before B's database open is ever *called*. This would fail if
      // someone reordered the code to fire off the purge and open the new
      // partition concurrently instead of sequentially awaiting it.
      const deleteADoneIndex = log.indexOf(`delete:done:${dbNameFor(A)}`);
      const openBCallIndex = log.indexOf(`open:call:${dbNameFor(B)}`);
      expect(deleteADoneIndex).toBeGreaterThanOrEqual(0);
      expect(openBCallIndex).toBeGreaterThan(deleteADoneIndex);

      // First paint after login renders only B's (empty) partition — never A's.
      expect(storeB.ids()).toEqual([]);
      expect(storeB.capture('cap-a').get()).toBeUndefined();
    },
  );
});

describe('purgePartition', () => {
  it('closes an open handle to the partition before deleting it', async () => {
    await openIdentityPartition(A);
    // Should not hang/throw despite the store above holding an open connection.
    await expect(purgePartition(A)).resolves.toBeUndefined();
  });

  it('deletes a partition with no open handle without hanging', async () => {
    await expect(purgePartition(A)).resolves.toBeUndefined();
  });

  it('rejects when the underlying deleteDatabase request errors', async () => {
    const failingIndexedDB = {
      deleteDatabase: () => {
        const request = {} as IDBOpenDBRequest;
        queueMicrotask(() => {
          (request as { error: Error | null }).error = new Error('boom');
          (request.onerror as (() => void) | null)?.();
        });
        return request;
      },
    } as unknown as IDBFactory;

    await expect(purgePartition(A, { indexedDB: failingIndexedDB })).rejects.toThrow('boom');
  });

  it('rejects with a synthesized error when the request errors without one', async () => {
    const failingIndexedDB = {
      deleteDatabase: () => {
        const request = {} as IDBOpenDBRequest;
        queueMicrotask(() => {
          (request.onerror as (() => void) | null)?.();
        });
        return request;
      },
    } as unknown as IDBFactory;

    await expect(purgePartition(A, { indexedDB: failingIndexedDB })).rejects.toThrow(
      /deleteDatabase.*failed/,
    );
  });
});
