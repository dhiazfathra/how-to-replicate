import type { Capture } from '../types/capture.js';

/** Invariant 1: the only viewable/exportable/routable state is `ready`. */
export function isViewable(capture: Capture): boolean {
  return capture.state === 'ready';
}

/** Throws unless `capture` is viewable. Call this before any export/share/route. */
export function assertReady(capture: Capture): void {
  if (!isViewable(capture)) {
    throw new Error(`capture "${capture.id}" is not ready (state: ${capture.state})`);
  }
}
