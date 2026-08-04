import type { Capture } from '../types/capture.js';
import type { CaptureRepository } from '../storage/repository.js';
import { createRecordStore, type Observable } from './observable.js';

export type CaptureStore = {
  /** Load every persisted capture into the store. Call once before use. */
  hydrate(): Promise<void>;
  /** Current capture ids, in hydration/insertion order. */
  ids(): string[];
  /** Observable for one capture record; `undefined` until hydrated/put. */
  capture(id: string): Observable<Capture | undefined>;
  /**
   * Apply a field mutation locally and synchronously, then persist. The
   * local write is never rolled back on a persistence failure (invariant 4):
   * instead the field is recorded in `sync.dirtyFields` and the error is
   * exposed via `persistError`.
   */
  setField<K extends keyof Capture>(id: string, field: K, value: Capture[K]): Promise<void>;
  /** Most recent persistence error for a given capture field, if any. */
  persistError(id: string, field: keyof Capture): Observable<Error | undefined>;
  putLocal(capture: Capture): void;
};

export function createCaptureStore(repo: CaptureRepository): CaptureStore {
  const records = createRecordStore<Capture | undefined>();
  const errors = createRecordStore<Error | undefined>();
  const order: string[] = [];

  function ensureTracked(id: string): void {
    if (!order.includes(id)) order.push(id);
  }

  function captureObservable(id: string): Observable<Capture | undefined> {
    return records.field(id, 'capture', undefined);
  }

  function putLocal(capture: Capture): void {
    ensureTracked(capture.id);
    captureObservable(capture.id).set(capture);
  }

  return {
    async hydrate(): Promise<void> {
      const captures = await repo.listCaptures();
      for (const capture of captures) putLocal(capture);
    },

    ids(): string[] {
      return [...order];
    },

    capture(id: string): Observable<Capture | undefined> {
      return captureObservable(id);
    },

    async setField<K extends keyof Capture>(
      id: string,
      field: K,
      value: Capture[K],
    ): Promise<void> {
      const observable = captureObservable(id);
      const current = observable.get();
      if (!current) {
        throw new Error(`setField: unknown capture "${id}"`);
      }

      const updated: Capture = { ...current, [field]: value };
      observable.set(updated); // local + synchronous, before persistence

      try {
        await repo.putCapture(updated);
        errors.field(id, field, undefined).set(undefined);
      } catch (err) {
        const error = err instanceof Error ? err : new Error(String(err));
        // No rollback: the local write stands. Mark dirty and surface the error.
        const dirtied: Capture = {
          ...updated,
          sync: {
            ...updated.sync,
            dirtyFields: updated.sync.dirtyFields.includes(field)
              ? updated.sync.dirtyFields
              : [...updated.sync.dirtyFields, field],
          },
        };
        observable.set(dirtied);
        errors.field(id, field, undefined).set(error);
      }
    },

    persistError(id: string, field: keyof Capture): Observable<Error | undefined> {
      return errors.field(id, field, undefined);
    },

    putLocal,
  };
}
