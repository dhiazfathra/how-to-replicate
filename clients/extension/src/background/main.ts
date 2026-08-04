import type { ChromeAdapter } from '../lib/chrome-adapter.js';
import { wireServiceWorker, type ActiveCapture } from './service-worker.js';

declare const chrome: ChromeAdapter;

// MV3 service-worker entry point (manifest `background.service_worker`).
// The real ambient `chrome` global satisfies `ChromeAdapter` structurally
// for the surface this extension uses; every actual behavior lives in
// `service-worker.ts`, tested against a stub adapter.
const worker = wireServiceWorker(chrome);

// The manifest declares no `default_popup`, so `action.onClicked` fires on
// every toolbar-icon click — this extension's only start/stop trigger
// (Critical-1 fix). One capture at a time: a click toggles it off if one is
// running, or starts one on the clicked tab's origin otherwise.
let active: ActiveCapture | null = null;

chrome.action.onClicked.addListener((tab) => {
  if (active) {
    const current = active;
    active = null;
    void worker.stop(current);
    return;
  }
  if (tab.id === undefined || !tab.url) return;
  void worker.start(new URL(tab.url).origin, { tabId: tab.id }).then((started) => {
    active = started;
  });
});
