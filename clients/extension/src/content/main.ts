import { createClock } from '@htr/capture-core';
import type { ChromeAdapter } from '../lib/chrome-adapter.js';
import { startInteractionTrail, type DocumentLike } from './interaction.js';
import { startBlurRegionTracking, type QueryableDocument, type RuntimePoster } from './blur-regions.js';

declare const chrome: ChromeAdapter;

// Content-script entry point (manifest `content_scripts`). Wiring only —
// `interaction.ts`/`blur-regions.ts` hold every testable behavior; this file
// just connects them to the real `document`/`chrome` globals a content
// script runs against.
const doc = document as unknown as DocumentLike & QueryableDocument;
const clock = createClock();
const captureId = chrome.runtime.getURL('').split('/').filter(Boolean).pop() ?? '';
const runtimePoster: RuntimePoster = {
  sendMessage: (message) => void chrome.runtime.sendMessage(message),
};

startInteractionTrail(doc, captureId, clock, () => window.location.href, (event) =>
  void chrome.runtime.sendMessage(event),
);

startBlurRegionTracking(doc, runtimePoster, { requestFrame: requestAnimationFrame }, captureId, []);
