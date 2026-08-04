import type { ChromeAdapter } from '../lib/chrome-adapter.js';
import { startRecorder, type CanvasTarget, type DegradeSink } from './recorder.js';
import type { MediaRecorderLike } from './timeslice.js';
import { createBlurRegionsListener, createFrameSource } from './wire.js';

/** Adapts the real `MediaRecorder` to the structural `MediaRecorderLike` seam `recorder.ts` drives. */
function wrapMediaRecorder(stream: MediaStream): MediaRecorderLike {
  const real = new MediaRecorder(stream, { mimeType: 'video/webm' });
  const wrapper: MediaRecorderLike = {
    get state() {
      return real.state;
    },
    start: (timesliceMs) => real.start(timesliceMs),
    stop: () => real.stop(),
    ondataavailable: null,
  };
  real.ondataavailable = (event): void => wrapper.ondataavailable?.({ data: event.data });
  return wrapper;
}

declare const chrome: ChromeAdapter;

// Offscreen-document entry point (manifest-declared via `chrome.offscreen`,
// wired by `service-worker.ts`'s `ensureOffscreenDocument`). Wiring only —
// `recorder.ts`/`wire.ts`/`blur.ts`/`frame-budget.ts`/`timeslice.ts`/
// `screenshot-fallback.ts` hold every testable behavior; this file just
// connects them to the real `chrome`/DOM globals an offscreen document runs
// against, and relays chunks/events back to the service worker (which owns
// the `InstantReplay` buffer) over `runtime.sendMessage`.
const captureId = new URLSearchParams(location.search).get('captureId') ?? '';

const sink: DegradeSink = {
  pushVideoChunk: (data) => void chrome.runtime.sendMessage({ type: 'htr:video-chunk', captureId, data }),
  pushScreenshot: (dataUrl) => void chrome.runtime.sendMessage({ type: 'htr:screenshot', captureId, dataUrl }),
  appendLifecycleEvent: (transition, detail) =>
    void chrome.runtime.sendMessage({ type: 'htr:lifecycle', captureId, transition, detail }),
  markDegraded: () => void chrome.runtime.sendMessage({ type: 'htr:degraded', captureId }),
};

async function main(): Promise<void> {
  const stream = await navigator.mediaDevices.getDisplayMedia({ video: true });
  const video = document.createElement('video');
  const canvas = document.createElement('canvas');
  const canvasTarget: CanvasTarget = {
    getContext: () => {
      const ctx = canvas.getContext('2d');
      if (!ctx) throw new Error('2d canvas context unavailable');
      return ctx;
    },
    captureStream: () => canvas.captureStream(),
  };
  const source = createFrameSource(video, stream);

  const handle = startRecorder(
    source,
    canvasTarget,
    (canvasStream) => wrapMediaRecorder(canvasStream as MediaStream),
    { requestFrame: requestAnimationFrame },
    { now: () => performance.now() },
    sink,
    { captureVisibleTab: () => chrome.tabs.captureVisibleTab() },
    { setInterval, clearInterval },
  );

  chrome.runtime.onMessage.addListener(createBlurRegionsListener(captureId, handle));
}

void main();
