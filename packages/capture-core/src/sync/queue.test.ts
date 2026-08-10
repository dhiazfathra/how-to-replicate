import { describe, expect, it } from 'vitest';
import { IDBFactory } from 'fake-indexeddb';
import { openCaptureDb } from '../storage/db.js';
import {
  createMutationQueue,
  estimateBytes,
  QUEUE_MUTATION_CAP,
  QUEUE_BYTE_CAP,
  QUEUE_WARN_RATIO,
  type QueuedMutation,
} from './queue.js';

function item(id: string, captureId = 'cap-1'): QueuedMutation {
  return {
    mutation: { id, captureId, op: { type: 'setTitle', title: `t-${id}` }, clientT: 0 },
    field: 'doc.title',
    base: '',
  };
}

async function freshQueue(name = `queue-test-${Math.random()}`) {
  const db = await openCaptureDb(name, { indexedDB: new IDBFactory() });
  return { db, queue: createMutationQueue(db) };
}

describe('createMutationQueue', () => {
  it('enqueues and lists in mutation-id (push) order', async () => {
    const { queue } = await freshQueue();
    await queue.enqueue(item('c'));
    await queue.enqueue(item('b'));
    await queue.enqueue(item('a'));
    const listed = await queue.list();
    expect(listed.map((i) => i.mutation.id)).toEqual(['a', 'b', 'c']);
  });

  it('filters listForField by captureId and field', async () => {
    const { queue } = await freshQueue();
    await queue.enqueue(item('a', 'cap-1'));
    await queue.enqueue(item('b', 'cap-2'));
    const forCap1 = await queue.listForField('cap-1', 'doc.title');
    expect(forCap1.map((i) => i.mutation.id)).toEqual(['a']);
  });

  it('removes a mutation by id', async () => {
    const { queue } = await freshQueue();
    await queue.enqueue(item('a'));
    await queue.remove('a');
    expect(await queue.list()).toEqual([]);
  });

  it('reports capacity totals, atCap, and nearCap', async () => {
    const { queue } = await freshQueue();
    expect(await queue.capacity()).toEqual({ count: 0, bytes: 0, atCap: false, nearCap: false });
    await queue.enqueue(item('a'));
    const cap = await queue.capacity();
    expect(cap.count).toBe(1);
    expect(cap.bytes).toBe(estimateBytes(item('a').mutation));
    expect(cap.atCap).toBe(false);
    expect(cap.nearCap).toBe(false);
  });

  it('hasRoom is true under both caps and false once the mutation count cap would be exceeded', async () => {
    const { queue } = await freshQueue();
    expect(await queue.hasRoom(1, 10)).toBe(true);
    expect(await queue.hasRoom(QUEUE_MUTATION_CAP + 1, 10)).toBe(false);
  });

  it('hasRoom is false once the byte cap would be exceeded', async () => {
    const { queue } = await freshQueue();
    expect(await queue.hasRoom(1, QUEUE_BYTE_CAP + 1)).toBe(false);
  });

  it('nearCap flips on once depth crosses the warn ratio (byte-driven, small count)', async () => {
    const { queue } = await freshQueue();
    // A single huge mutation crosses the byte warn ratio without touching the count cap.
    const bigTitle = 'x'.repeat(Math.ceil(QUEUE_BYTE_CAP * QUEUE_WARN_RATIO));
    await queue.enqueue({
      mutation: { id: 'huge', captureId: 'cap-1', op: { type: 'setTitle', title: bigTitle }, clientT: 0 },
      field: 'doc.title',
      base: '',
    });
    const cap = await queue.capacity();
    expect(cap.nearCap).toBe(true);
    expect(cap.atCap).toBe(false);
  });

  it('survives a simulated restart (reopen against the same underlying indexedDB factory)', async () => {
    const factory = new IDBFactory();
    const dbName = `restart-${Math.random()}`;
    const db1 = await openCaptureDb(dbName, { indexedDB: factory });
    await createMutationQueue(db1).enqueue(item('a'));
    db1.close();

    const db2 = await openCaptureDb(dbName, { indexedDB: factory });
    const reopened = createMutationQueue(db2);
    expect((await reopened.list()).map((i) => i.mutation.id)).toEqual(['a']);
  });
});
