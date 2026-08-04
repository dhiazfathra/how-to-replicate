import { createClock, type EnvSnapshot } from '@htr/capture-core';
import {
  finalizeRecording,
  RecordingFinalizeError,
  startRecording,
  type CanvasTarget,
  type MediaRecorderLike,
  type RecorderChunk,
} from './capture-flow.js';
import { BUNDLED_RULESET } from './ruleset.js';

// Wiring only, against real `getDisplayMedia`/`MediaRecorder`/DOM globals —
// every branch of actual behavior lives in `capture-flow.ts`, which is
// exercised with fakes and fully covered (see vitest.config.ts's exclude).
//
// Unlike the extension, this page has no content script, so it never learns
// live DOM-selector blur regions for whatever the user chose to share via
// `getDisplayMedia` (a whole screen, window, or tab it doesn't control the
// content of). `startRecording` always composites with an empty region
// list — the pre-encode canvas relay still runs, so invariant 2's "blur
// feeds MediaRecorder, never the raw stream" shape holds even though there
// is nothing to blur yet.

const startButton = document.getElementById('start') as HTMLButtonElement;
const stopButton = document.getElementById('stop') as HTMLButtonElement;
const statusEl = document.getElementById('status') as HTMLElement;
const downloadLink = document.getElementById('download') as HTMLAnchorElement;

function envSnapshot(): EnvSnapshot {
  return {
    userAgent: navigator.userAgent,
    platform: navigator.platform,
    viewport: { w: window.innerWidth, h: window.innerHeight },
    devicePixelRatio: window.devicePixelRatio,
    locale: navigator.language,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    url: location.href,
  };
}

/** Adapts the real `MediaRecorder` to the structural `MediaRecorderLike` seam `capture-flow.ts` drives. */
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

let stopHandle: { stop(): void } | null = null;
let displayStream: MediaStream | null = null;

async function start(): Promise<void> {
  startButton.disabled = true;
  statusEl.textContent = 'Recording…';
  downloadLink.hidden = true;

  displayStream = await navigator.mediaDevices.getDisplayMedia({ video: true });
  const video = document.createElement('video');
  video.srcObject = displayStream;
  await video.play();

  const canvas = document.createElement('canvas');
  canvas.width = video.videoWidth || 1280;
  canvas.height = video.videoHeight || 720;
  const canvasTarget: CanvasTarget = {
    getContext: () => {
      const ctx = canvas.getContext('2d');
      if (!ctx) throw new Error('2d canvas context unavailable');
      return ctx;
    },
    captureStream: () => canvas.captureStream(),
  };

  const chunks: RecorderChunk[] = [];
  stopHandle = startRecording({
    source: { get width() { return canvas.width; }, get height() { return canvas.height; }, get frame() { return video; } },
    canvas: canvasTarget,
    createMediaRecorder: (canvasStream) => wrapMediaRecorder(canvasStream as MediaStream),
    scheduler: { requestFrame: requestAnimationFrame },
    onChunk: (chunk) => chunks.push(chunk),
  });

  stopButton.disabled = false;
  stopButton.onclick = () => void stop(chunks);
}

async function stop(chunks: RecorderChunk[]): Promise<void> {
  stopButton.disabled = true;
  stopHandle?.stop();
  displayStream?.getTracks().forEach((track) => track.stop());
  statusEl.textContent = 'Redacting and building your download…';

  try {
    const { bundle } = await finalizeRecording({
      ruleset: BUNDLED_RULESET,
      chunks,
      env: envSnapshot(),
      clock: createClock(),
    });

    const blob = new Blob([bundle as BlobPart], { type: 'application/zip' });
    downloadLink.href = URL.createObjectURL(blob);
    downloadLink.hidden = false;
    statusEl.textContent = 'Ready — nothing has been transmitted. Click Download to save it.';
  } catch (error) {
    const message = error instanceof RecordingFinalizeError ? error.message : String(error);
    statusEl.textContent = `Recording failed: ${message}`;
  } finally {
    startButton.disabled = false;
  }
}

startButton.addEventListener('click', () => void start());
