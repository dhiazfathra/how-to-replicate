import { useSyncExternalStore } from 'react';
import type { Capture, CaptureStore } from '@htr/capture-core';

/**
 * Subscribes to exactly one capture's observable. Because `CaptureStore`
 * keys its observables per capture id, a mutation to capture B never
 * notifies a component bound to capture A — one delta re-renders one cell.
 */
export function useCapture(store: CaptureStore, id: string): Capture | undefined {
  const observable = store.capture(id);
  return useSyncExternalStore(
    (onChange) => observable.subscribe(onChange),
    () => observable.get(),
  );
}

/**
 * The list of known capture ids. This does not change per-field (ids() is a
 * snapshot of insertion order), so it's read once per render rather than
 * subscribed — callers combine it with `useCapture` per row for granular
 * updates.
 */
export function useCaptureIds(store: CaptureStore): string[] {
  return store.ids();
}

export function usePersistError(store: CaptureStore, id: string, field: keyof Capture) {
  const observable = store.persistError(id, field);
  return useSyncExternalStore(
    (onChange) => observable.subscribe(onChange),
    () => observable.get(),
  );
}
