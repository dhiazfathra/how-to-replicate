import type { DBSchema } from 'idb';
import type { Capture } from '../types/capture.js';
import type { CaptureEvent } from '../types/event.js';
import type { AssetRef } from '../types/asset.js';
import type { RedactionRuleset } from '../redaction/ruleset.js';

export type AssetChunk = {
  assetId: string;
  seq: number;
  bytes: Uint8Array;
};

export interface CaptureDbSchema extends DBSchema {
  captures: {
    key: string;
    value: Capture;
  };
  events: {
    key: string;
    value: CaptureEvent;
    indexes: { 'by-capture': [string, number] };
  };
  assets: {
    key: string;
    value: AssetRef;
    indexes: { 'by-capture': string };
  };
  asset_chunks: {
    key: [string, number];
    value: AssetChunk;
  };
  rulesets: {
    key: string;
    value: RedactionRuleset;
  };
}

export const DB_VERSION = 1;

export function upgradeCaptureDb(db: import('idb').IDBPDatabase<CaptureDbSchema>): void {
  db.createObjectStore('captures', { keyPath: 'id' });

  const events = db.createObjectStore('events', { keyPath: 'id' });
  events.createIndex('by-capture', ['captureId', 't']);

  const assets = db.createObjectStore('assets', { keyPath: 'id' });
  assets.createIndex('by-capture', 'captureId');

  db.createObjectStore('asset_chunks', { keyPath: ['assetId', 'seq'] });

  db.createObjectStore('rulesets', { keyPath: 'version' });
}
