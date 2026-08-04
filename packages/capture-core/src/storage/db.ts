import { openDB, type IDBPDatabase } from 'idb';
import { DB_VERSION, upgradeCaptureDb, type CaptureDbSchema } from './schema.js';

export const DEFAULT_DB_NAME = 'how-to-replicate';

export type OpenCaptureDbOptions = {
  /** Injectable so tests can point at fake-indexeddb instead of a real browser. */
  indexedDB?: IDBFactory;
};

export function openCaptureDb(
  name = DEFAULT_DB_NAME,
  options: OpenCaptureDbOptions = {},
): Promise<IDBPDatabase<CaptureDbSchema>> {
  return openDB<CaptureDbSchema>(name, DB_VERSION, {
    upgrade: upgradeCaptureDb,
    ...(options.indexedDB ? { indexedDB: options.indexedDB } : {}),
  });
}
