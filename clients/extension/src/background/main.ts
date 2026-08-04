import type { ChromeAdapter } from '../lib/chrome-adapter.js';
import { wireServiceWorker } from './service-worker.js';
import { createCaptureController } from './capture-controller.js';

declare const chrome: ChromeAdapter;

// MV3 service-worker entry point (manifest `background.service_worker`).
// The real ambient `chrome` global satisfies `ChromeAdapter` structurally
// for the surface this extension uses; every actual behavior lives in
// `service-worker.ts` and `capture-controller.ts`, tested against a stub
// adapter.
const worker = wireServiceWorker(chrome);
const controller = createCaptureController(worker);

// The manifest declares no `default_popup`, so `action.onClicked` fires on
// every toolbar-icon click — this extension's only start/stop trigger
// (Critical-1 fix).
chrome.action.onClicked.addListener((tab) => {
  void controller.onClicked(tab);
});
