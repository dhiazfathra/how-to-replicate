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
    const tx = this.db.transaction('events', 'readwrite');
    await Promise.all(
      events.map((event) => tx.store.put({ ...event, captureId })),
    );
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

  async deleteCapture(id: string): Promise<void> {
    const assets = await this.db.getAllFromIndex('assets', 'by-capture', id);
    const events = await this.readEvents(id);

    const tx = this.db.transaction(
      ['captures', 'events', 'assets', 'asset_chunks'],
      'readwrite',
    );
    const ops: Promise<unknown>[] = [tx.objectStore('captures').delete(id)];
    for (const event of events) {
      ops.push(tx.objectStore('events').delete(event.id));
    }
    for (const asset of assets) {
      ops.push(tx.objectStore('assets').delete(asset.id));
      ops.push(
        tx
          .objectStore('asset_chunks')
          .delete(IDBKeyRange.bound([asset.id, 0], [asset.id, Infinity])),
      );
    }
    await Promise.all(ops);
    await tx.done;
  }
}
