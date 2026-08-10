import type { IDBPDatabase } from 'idb';
import type { CaptureDbSchema } from './schema.js';
import type { Capture } from '../types/capture.js';
import type { CaptureEvent } from '../types/event.js';
import type { AssetRef } from '../types/asset.js';

export const DEFAULT_CHUNK_BYTES = 1024 * 1024;

export class CaptureRepository {
  constructor(private readonly db: IDBPDatabase<CaptureDbSchema>) {}

  async putCapture(capture: Capture): Promise<void> {
    await this.db.put('captures', capture);
  }

  async getCapture(id: string): Promise<Capture | undefined> {
    return this.db.get('captures', id);
  }

  async listCaptures(): Promise<Capture[]> {
    return this.db.getAll('captures');
  }

  async appendEvents(captureId: string, events: CaptureEvent[]): Promise<void> {
    const mismatch = events.find((event) => event.captureId !== captureId);
    if (mismatch) {
      throw new Error(
        `appendEvents: event "${mismatch.id}" has captureId "${mismatch.captureId}", expected "${captureId}"`,
      );
    }
    const tx = this.db.transaction('events', 'readwrite');
    await Promise.all(events.map((event) => tx.store.put(event)));
    await tx.done;
  }

  async readEvents(captureId: string): Promise<CaptureEvent[]> {
    const range = IDBKeyRange.bound([captureId, -Infinity], [captureId, Infinity]);
    return this.db.getAllFromIndex('events', 'by-capture', range);
  }

  async putAssetChunked(
    assetRef: AssetRef,
    blob: Blob | Uint8Array,
    chunkBytes = DEFAULT_CHUNK_BYTES,
  ): Promise<void> {
    // ponytail: holds the full blob plus every chunk slice in memory at once;
    // fine at Phase 0 asset sizes, revisit with a streaming writer if assets
    // grow past what a tab's heap can hold.
    const bytes = blob instanceof Uint8Array ? blob : new Uint8Array(await blob.arrayBuffer());
    const chunkCount = bytes.byteLength === 0 ? 1 : Math.ceil(bytes.byteLength / chunkBytes);

    const tx = this.db.transaction(['assets', 'asset_chunks'], 'readwrite');
    await tx.objectStore('assets').put({ ...assetRef, chunkCount });
    const chunkStore = tx.objectStore('asset_chunks');
    const puts: Promise<unknown>[] = [];
    for (let seq = 0; seq < chunkCount; seq += 1) {
      const start = seq * chunkBytes;
      puts.push(
        chunkStore.put({
          assetId: assetRef.id,
          seq,
          bytes: bytes.slice(start, start + chunkBytes),
        }),
      );
    }
    await Promise.all(puts);
    await tx.done;
  }

  async readAsset(assetId: string): Promise<Uint8Array | undefined> {
    const asset = await this.db.get('assets', assetId);
    if (!asset) return undefined;

    const range = IDBKeyRange.bound([assetId, 0], [assetId, Infinity]);
    const rows = await this.db.getAll('asset_chunks', range);

    const sorted = [...rows].sort((a, b) => a.seq - b.seq);
    const total = sorted.reduce((sum, chunk) => sum + chunk.bytes.byteLength, 0);
    const out = new Uint8Array(total);
    let offset = 0;
    for (const chunk of sorted) {
      out.set(chunk.bytes, offset);
      offset += chunk.bytes.byteLength;
    }
    return out;
  }

  /** Persisted `PullDeltas(since=revision)` cursor for a workspace. `0` if never pulled. */
  async getSyncCursor(workspaceId: string): Promise<number> {
    const cursor = await this.db.get('sync_cursor', workspaceId);
    return cursor?.revision ?? 0;
  }

  async putSyncCursor(workspaceId: string, revision: number): Promise<void> {
    await this.db.put('sync_cursor', { workspaceId, revision });
  }

  async deleteCapture(id: string): Promise<void> {
    // Single readwrite transaction spanning lookup and delete: the cascade
    // set is computed from a snapshot IndexedDB guarantees can't change
    // underneath this transaction, so a concurrent appendEvents/putAssetChunked
    // for the same capture either lands before this transaction starts (and
    // is included in the cascade) or blocks until this transaction commits
    // (and then writes to an already-deleted capture) — never orphaned rows.
    const tx = this.db.transaction(
      ['captures', 'events', 'assets', 'asset_chunks'],
      'readwrite',
    );
    const eventsStore = tx.objectStore('events');
    const assetsStore = tx.objectStore('assets');
    const range = IDBKeyRange.bound([id, -Infinity], [id, Infinity]);

    const [eventKeys, assets] = await Promise.all([
      eventsStore.index('by-capture').getAllKeys(range),
      assetsStore.index('by-capture').getAllKeys(id),
    ]);

    const ops: Promise<unknown>[] = [
      tx.objectStore('captures').delete(id),
      ...eventKeys.map((key) => eventsStore.delete(key)),
      ...assets.map((assetId) => assetsStore.delete(assetId)),
      ...assets.map((assetId) =>
        tx
          .objectStore('asset_chunks')
          .delete(IDBKeyRange.bound([assetId, 0], [assetId, Infinity])),
      ),
    ];
    await Promise.all(ops);
    await tx.done;
  }

  /**
   * Eviction: removes a capture's local events/assets/chunks but keeps the
   * capture metadata row, marked `localAssets: false`, so the viewer can
   * offer to re-fetch from the server instead of showing a broken capture.
   * Never deletes the capture record itself — see deleteCapture for that.
   */
  async evictLocalAssets(id: string): Promise<void> {
    const tx = this.db.transaction(
      ['captures', 'events', 'assets', 'asset_chunks'],
      'readwrite',
    );
    const capturesStore = tx.objectStore('captures');
    const eventsStore = tx.objectStore('events');
    const assetsStore = tx.objectStore('assets');
    const range = IDBKeyRange.bound([id, -Infinity], [id, Infinity]);

    const [capture, eventKeys, assets] = await Promise.all([
      capturesStore.get(id),
      eventsStore.index('by-capture').getAllKeys(range),
      assetsStore.index('by-capture').getAllKeys(id),
    ]);

    const ops: Promise<unknown>[] = [
      ...eventKeys.map((key) => eventsStore.delete(key)),
      ...assets.map((assetId) => assetsStore.delete(assetId)),
      ...assets.map((assetId) =>
        tx
          .objectStore('asset_chunks')
          .delete(IDBKeyRange.bound([assetId, 0], [assetId, Infinity])),
      ),
    ];
    if (capture) {
      ops.push(capturesStore.put({ ...capture, assets: [], localAssets: false }));
    }
    await Promise.all(ops);
    await tx.done;
  }
}
