import 'fake-indexeddb/auto';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { IDBFactory } from 'fake-indexeddb';
import { getRecordedIdentity, type Identity } from '@htr/capture-core';
import { bootstrapSession, login, logout } from './identity.js';

/**
 * Integration test for the real viewer bootstrap path (identity.ts, the
 * module main.tsx actually calls) — not just the isolated capture-core
 * partition unit tests. Proves that logging out of subject A and into
 * subject B never leaves A's data readable, including at first paint of the
 * next session.
 */
describe('viewer identity bootstrap', () => {
  beforeEach(() => {
    vi.stubGlobal('indexedDB', new IDBFactory());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const A: Identity = { subject: 'alice', workspaceId: 'w1' };
  const B: Identity = { subject: 'bob', workspaceId: 'w1' };

  it('has no data to render before any identity has ever logged in', async () => {
    const session = await bootstrapSession();
    expect(session).toBeNull();
  });

  it(
    'logout then login as a different subject renders nothing from the ' +
      "first subject's partition, ever, including first paint",
    async () => {
      const sessionA = await login(A);
      sessionA.store.putLocal({
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
      await sessionA.store.setField('cap-a', 'state', 'ready');
      expect(sessionA.store.ids()).toEqual(['cap-a']);

      // Logout closes and purges A's partition before anything new opens.
      await logout();
      expect(await getRecordedIdentity()).toBeNull();

      // A fresh bootstrap immediately after logout (simulating first paint
      // before B has authenticated) must not resurrect A's data.
      expect(await bootstrapSession()).toBeNull();

      // Login as B: first paint's session must be B's empty partition only.
      const sessionB = await login(B);
      expect(sessionB.store.ids()).toEqual([]);
      expect(sessionB.store.capture('cap-a').get()).toBeUndefined();

      // Re-bootstrapping (e.g. a page reload while B is logged in) also
      // never resurrects A.
      const rebooted = await bootstrapSession();
      expect(rebooted?.store.ids()).toEqual([]);
    },
  );
});
