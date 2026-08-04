import type { ChromeAdapter } from '../lib/chrome-adapter.js';
import { wireServiceWorker } from './service-worker.js';

declare const chrome: ChromeAdapter;

// MV3 service-worker entry point (manifest `background.service_worker`).
// The real ambient `chrome` global satisfies `ChromeAdapter` structurally
// for the surface this extension uses; every actual behavior lives in
// `service-worker.ts`, tested against a stub adapter.
wireServiceWorker(chrome);
