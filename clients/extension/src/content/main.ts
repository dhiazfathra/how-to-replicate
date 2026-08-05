import type { ChromeAdapter } from '../lib/chrome-adapter.js';
import { startInteractionTrail, type DocumentLike } from './interaction.js';
import { startBlurRegionTracking, type QueryableDocument, type RuntimePoster } from './blur-regions.js';
import { createAnchoredClock, createSessionListener } from './session.js';

declare const chrome: ChromeAdapter;

// Content-script entry point (manifest `content_scripts`). Wiring only —
// `interaction.ts`/`blur-regions.ts`/`session.ts` hold every testable
// behavior; this file just connects them to the real `document`/`chrome`
// globals a content script runs against. Neither the interaction trail nor
// the blur-region rAF loop runs until the service worker pushes a
// `htr:capture-start` message (there is no captureId to attribute anything
// to before that) — both tear down again on `htr:capture-stop`.
const doc = document as unknown as DocumentLike & QueryableDocument;
const runtimePoster: RuntimePoster = {
  sendMessage: (message) => void chrome.runtime.sendMessage(message),
};

let stopInteractionTrail: (() => void) | null = null;
let stopBlurTracking: (() => void) | null = null;

chrome.runtime.onMessage.addListener(
  createSessionListener({
    onStart({ captureId, epoch, blurSelectors }) {
      const clock = createAnchoredClock(epoch);
      stopInteractionTrail = startInteractionTrail(doc, captureId, clock, () => window.location.href, (event) =>
        void chrome.runtime.sendMessage(event),
      );
      stopBlurTracking = startBlurRegionTracking(
        doc,
        runtimePoster,
        // Wrapped, not passed bare: the scheduler seam calls this as
        // `scheduler.requestFrame(...)`, and a `requestAnimationFrame`
        // reference invoked with an object `this` throws "Illegal invocation".
        { requestFrame: (callback) => window.requestAnimationFrame(callback) },
        captureId,
        blurSelectors,
      );
    },
    onStop() {
      stopInteractionTrail?.();
      stopInteractionTrail = null;
      stopBlurTracking?.();
      stopBlurTracking = null;
    },
  }),
);
