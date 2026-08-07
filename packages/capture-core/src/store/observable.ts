export type Observable<T> = {
  get(): T;
  set(next: T): void;
  subscribe(fn: (value: T) => void): () => void;
};

/**
 * Minimal, dependency-free per-field observable (spec §16 "granular
 * observables"). Subscribers fire synchronously on `set`. A `set` whose value
 * is `Object.is`-equal to the current value is a no-op: nobody is notified.
 */
export function createObservable<T>(initial: T): Observable<T> {
  let value = initial;
  const subscribers = new Set<(value: T) => void>();

  return {
    get(): T {
      return value;
    },
    set(next: T): void {
      if (Object.is(value, next)) return;
      value = next;
      for (const fn of subscribers) fn(value);
    },
    subscribe(fn: (value: T) => void): () => void {
      subscribers.add(fn);
      return () => subscribers.delete(fn);
    },
  };
}

/**
 * Per-entity, per-field observables: `store.field(entityId, fieldName)`
 * lazily creates (or returns) the observable for that cell, so a delta
 * re-renders one cell rather than the whole record/list.
 */
export function createRecordStore<T>() {
  const fields = new Map<string, Map<string, Observable<T>>>();

  function field(entityId: string, fieldName: string, initial: T): Observable<T> {
    let entity = fields.get(entityId);
    if (!entity) {
      entity = new Map();
      fields.set(entityId, entity);
    }
    let observable = entity.get(fieldName);
    if (!observable) {
      observable = createObservable(initial);
      entity.set(fieldName, observable);
    }
    return observable;
  }

  function deleteEntity(entityId: string): void {
    fields.delete(entityId);
  }

  return { field, deleteEntity };
}
