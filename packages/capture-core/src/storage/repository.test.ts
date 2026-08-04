import { beforeEach, describe, expect, it } from 'vitest';
import { IDBFactory } from 'fake-indexeddb';
import { openCaptureDb } from './db.js';
import { CaptureRepository, DEFAULT_CHUNK_BYTES } from './repository.js';
import type { Capture } from '../types/capture.js';
import type { CaptureEvent } from '../types/event.js';
import type { AssetRef } from '../types/asset.js';

function makeCapture(id: string): Capture {
  return {
    id,
    workspaceId: null,
    projectId: null,
    source: 'extension',
    state: 'recording',
    fidelity: 'full',
    createdAt: '2026-08-04T00:00:00.000Z',
    epoch: 0,
    env: {
      userAgent: 'test-agent',
      platform: 'test',
      viewport: { w: 100, h: 100 },
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

function makeEvent(id: string, captureId: string, t: number): CaptureEvent {
  return {
    id,
    captureId,
    t,
    kind: 'annotation',
    payload: { text: `event-${id}` },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function makeAssetRef(id: string, captureId: string, sizeBytes: number): AssetRef {
  return {
    id,
    captureId,
    kind: 'screenshot',
    mimeType: 'image/png',
    sizeBytes,
    chunkCount: 0,
    sha256: 'deadbeef',
  };
}

async function freshRepository(): Promise<CaptureRepository> {
  const db = await openCaptureDb(`repo-test-${Math.random()}`, { indexedDB: new IDBFactory() });
  return new CaptureRepository(db);
}

describe('CaptureRepository', () => {
  let repo: CaptureRepository;

  beforeEach(async () => {
    repo = await freshRepository();
  });

  it('round-trips a capture through put/get', async () => {
    const capture = makeCapture('cap-1');
    await repo.putCapture(capture);
    await expect(repo.getCapture('cap-1')).resolves.toEqual(capture);
  });

  it('returns undefined for a missing capture', async () => {
    await expect(repo.getCapture('missing')).resolves.toBeUndefined();
  });

  it('lists all captures', async () => {
    await repo.putCapture(makeCapture('cap-1'));
    await repo.putCapture(makeCapture('cap-2'));
    const listed = await repo.listCaptures();
    expect(listed.map((c) => c.id).sort()).toEqual(['cap-1', 'cap-2']);
  });

  it('appends events in a single transaction', async () => {
    await repo.putCapture(makeCapture('cap-1'));
    await repo.appendEvents('cap-1', [
      makeEvent('evt-1', 'cap-1', 10),
      makeEvent('evt-2', 'cap-1', 20),
    ]);
    const events = await repo.readEvents('cap-1');
    expect(events).toHaveLength(2);
  });

  it('reads events ordered by t then id', async () => {
    await repo.putCapture(makeCapture('cap-1'));
    await repo.appendEvents('cap-1', [
      makeEvent('evt-b', 'cap-1', 5),
      makeEvent('evt-a', 'cap-1', 5),
      makeEvent('evt-z', 'cap-1', 1),
    ]);
    const events = await repo.readEvents('cap-1');
    expect(events.map((e) => e.id)).toEqual(['evt-z', 'evt-a', 'evt-b']);
  });

  it('scopes readEvents to the given capture', async () => {
    await repo.putCapture(makeCapture('cap-1'));
    await repo.putCapture(makeCapture('cap-2'));
    await repo.appendEvents('cap-1', [makeEvent('evt-1', 'cap-1', 1)]);
    await repo.appendEvents('cap-2', [makeEvent('evt-2', 'cap-2', 1)]);
    const events = await repo.readEvents('cap-1');
    expect(events.map((e) => e.id)).toEqual(['evt-1']);
  });

  it('round-trips a chunked asset larger than one chunk', async () => {
    const bytes = new Uint8Array(DEFAULT_CHUNK_BYTES * 2 + 100);
    for (let i = 0; i < bytes.length; i += 1) bytes[i] = i % 256;
    const ref = makeAssetRef('asset-1', 'cap-1', bytes.byteLength);

    await repo.putCapture(makeCapture('cap-1'));
    await repo.putAssetChunked(ref, bytes);

    const read = await repo.readAsset('asset-1');
    expect(read).toEqual(bytes);
  }, 20_000);

  it('honors a custom chunk size', async () => {
    const bytes = new Uint8Array([1, 2, 3, 4, 5]);
    const ref = makeAssetRef('asset-2', 'cap-1', bytes.byteLength);
    await repo.putAssetChunked(ref, bytes, 2);
    const read = await repo.readAsset('asset-2');
    expect(read).toEqual(bytes);
  });

  it('accepts a Blob and chunks empty content as a single empty chunk', async () => {
    const blob = new Blob([]);
    const ref = makeAssetRef('asset-3', 'cap-1', 0);
    await repo.putAssetChunked(ref, blob);
    const read = await repo.readAsset('asset-3');
    expect(read).toEqual(new Uint8Array(0));
  });

  it('returns undefined reading an asset that does not exist', async () => {
    await expect(repo.readAsset('missing')).resolves.toBeUndefined();
  });

  it('cascades delete across events, assets, and asset chunks', async () => {
    const capture = makeCapture('cap-1');
    await repo.putCapture(capture);
    await repo.appendEvents('cap-1', [makeEvent('evt-1', 'cap-1', 1)]);
    await repo.putAssetChunked(makeAssetRef('asset-1', 'cap-1', 5), new Uint8Array([1, 2, 3, 4, 5]), 2);

    await repo.deleteCapture('cap-1');

    await expect(repo.getCapture('cap-1')).resolves.toBeUndefined();
    await expect(repo.readEvents('cap-1')).resolves.toEqual([]);
    await expect(repo.readAsset('asset-1')).resolves.toBeUndefined();
  });

  it('deleting a capture with no events or assets is a no-op beyond removing the capture', async () => {
    await repo.putCapture(makeCapture('cap-1'));
    await repo.deleteCapture('cap-1');
    await expect(repo.getCapture('cap-1')).resolves.toBeUndefined();
  });
});
