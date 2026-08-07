import { describe, expect, it } from 'vitest';
import { IDBFactory } from 'fake-indexeddb';
import { openCaptureDb } from './db.js';

describe('openCaptureDb', () => {
  it('opens a database with all object stores and indexes', async () => {
    const db = await openCaptureDb('test-db-1', { indexedDB: new IDBFactory() });
    expect([...db.objectStoreNames].sort()).toEqual(
      ['asset_chunks', 'assets', 'captures', 'events', 'rulesets'].sort(),
    );
    const tx = db.transaction('events', 'readonly');
    expect([...tx.store.indexNames]).toContain('by-capture');
    db.close();
  });

  it('defaults to the shared indexedDB factory and db name', async () => {
    const db = await openCaptureDb();
    expect(db.name).toBe('how-to-replicate');
    db.close();
  });
});
