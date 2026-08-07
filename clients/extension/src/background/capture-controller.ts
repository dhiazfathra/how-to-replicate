import type { ChromeActionClickTab } from '../lib/chrome-adapter.js';
import type { ActiveCapture, ServiceWorker } from './service-worker.js';

export type CaptureController = {
  /**
   * Handle one toolbar-icon click. Toggles capture off if one is running, or
   * starts one on the clicked tab's origin otherwise. Clicks that arrive
   * while a start or stop is already in flight are ignored — `start()` is
   * async, so without this guard a second click could race the `active`
   * assignment and leak the first capture (Minor fix).
   */
  onClicked(tab: ChromeActionClickTab): Promise<void>;
};

/**
 * Wraps a `ServiceWorker` with the single-capture-at-a-time click policy.
 * Lives outside `main.ts` (excluded from coverage as pure ambient-global
 * wiring) so this branching/race-guard logic stays covered and testable.
 */
export function createCaptureController(worker: ServiceWorker): CaptureController {
  let active: ActiveCapture | null = null;
  let busy = false;

  return {
    async onClicked(tab: ChromeActionClickTab): Promise<void> {
      if (busy) return;
      busy = true;
      try {
        if (active) {
          const current = active;
          active = null;
          await worker.stop(current);
          return;
        }
        if (tab.id === undefined || !tab.url) return;
        active = await worker.start(new URL(tab.url).origin, { tabId: tab.id });
      } finally {
        busy = false;
      }
    },
  };
}
