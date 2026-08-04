import type { ChromeAdapter } from '../lib/chrome-adapter.js';
import { encodeChunk } from '../lib/chunk-codec.js';
import { startRecorder, type CanvasTarget, type DegradeSink } from './recorder.js';
import type { MediaRecorderLike } from './timeslice.js';
import { createBlurRegionsListener, createFrameSource } from './wire.js';
import { CAPTURE_STOP_MESSAGE_TYPE, type CaptureStopMessage } from '../content/session.js';

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
    onstop: null,
  };
  real.ondataavailable = (event): void => wrapper.ondataavailable?.({ data: event.data });
  real.onstop = (): void => wrapper.onstop?.();
  return wrapper;
}

function isStopMessage(message: unknown): message is CaptureStopMessage {
  return (
    typeof message === 'object' &&
    message !== null &&
    (message as { type?: unknown }).type === CAPTURE_STOP_MESSAGE_TYPE
  );
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
  // `chrome.runtime.sendMessage` JSON-serializes its payload — a raw
  // Uint8Array/Blob would arrive on the service-worker side as `{}`. Encode
  // to base64 here; `offscreen-relay.ts` decodes it back before it ever
  // reaches `pushVideoChunk`.
  pushVideoChunk: (data) =>
    void encodeChunk(data).then((encoded) =>
      chrome.runtime.sendMessage({ type: 'htr:video-chunk', captureId, data: encoded }),
    ),
  pushScreenshot: (dataUrl) => void chrome.runtime.sendMessage({ type: 'htr:screenshot', captureId, dataUrl }),
  appendLifecycleEvent: (transition, detail) =>
    void chrome.runtime.sendMessage({ type: 'htr:lifecycle', captureId, transition, detail }),
  markDegraded: () => void chrome.runtime.sendMessage({ type: 'htr:degraded', captureId }),
};

// Offscreen documents cannot use the tabs API (MV3 only allows
// `chrome.runtime` plus a small allowlist here) — relay the screenshot
// capture to the service worker instead, which does have tabs access.
const screenshot = {
  captureVisibleTab: (): Promise<string> =>
    chrome.runtime
      .sendMessage({ type: 'htr:capture-screenshot-request', captureId })
      .then((response) => response as string),
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
    screenshot,
    { setInterval, clearInterval },
  );

  chrome.runtime.onMessage.addListener(createBlurRegionsListener(captureId, handle));

  // Service-worker's stop() awaits this ack before finalizing the capture —
  // without it, the recorder's final pending chunk can race document
  // teardown and be dropped silently (invariant 4).
  chrome.runtime.onMessage.addListener(
    (message: unknown, _sender: unknown, sendResponse: (r?: unknown) => void): boolean | void => {
      if (!isStopMessage(message) || message.captureId !== captureId) return;
      void handle.stop().then(() => sendResponse(true));
      return true;
    },
  );
}

void main();
