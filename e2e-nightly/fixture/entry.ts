/**
 * In-page nightly video-blur fixture. Draws known synthetic PHI text onto a
 * canvas, blurs it with the real `compositeFrame`/`validateRegions`
 * (`@htr/capture-core`, the same pre-encode blur the offscreen recorder
 * drives — see `clients/extension/src/offscreen/recorder.ts`), records the
 * canvas's own `MediaStream` with a real `MediaRecorder` into an actual
 * video/webm blob, then decodes stills back out of that recording through a
 * real `<video>` element. The Node-side spec OCRs the decoded stills.
 *
 * Slow (record + decode + OCR per run), which is exactly why this lives in
 * its own nightly job instead of the PR-gating `e2e/` suite.
 */
import { compositeFrame, validateRegions, type RectLike } from '@htr/capture-core';
import type { BlurFixture, BlurFixtureResult } from './api.js';

const WIDTH = 640;
const HEIGHT = 360;
const RECORD_MS = 600;
/** Where the PHI text is drawn — also the region a real ruleset would blur. */
const TEXT_RECT: RectLike = { x: 20, y: 100, width: 500, height: 90 };
const DECODE_TIMES_S = [0.1, 0.3];

function drawSource(canvas: HTMLCanvasElement, text: string): void {
  const ctx = canvas.getContext('2d')!;
  ctx.fillStyle = '#ffffff';
  ctx.fillRect(0, 0, WIDTH, HEIGHT);
  ctx.fillStyle = '#000000';
  ctx.font = 'bold 40px monospace';
  ctx.textBaseline = 'top';
  ctx.fillText(text, TEXT_RECT.x, TEXT_RECT.y);
}

async function recordWebm(outputCanvas: HTMLCanvasElement): Promise<Blob> {
  const stream = outputCanvas.captureStream(10);
  const recorder = new MediaRecorder(stream, { mimeType: 'video/webm' });
  const chunks: Blob[] = [];
  recorder.ondataavailable = (e) => {
    if (e.data.size > 0) chunks.push(e.data);
  };
  const stopped = new Promise<void>((resolve) => {
    recorder.onstop = () => resolve();
  });
  recorder.start();
  await new Promise((resolve) => setTimeout(resolve, RECORD_MS));
  recorder.stop();
  await stopped;
  return new Blob(chunks, { type: 'video/webm' });
}

/** Decode real frames back out of a recorded blob via a real `<video>` element. */
async function decodeFrames(blob: Blob): Promise<string[]> {
  const video = document.createElement('video');
  video.muted = true;
  video.src = URL.createObjectURL(blob);
  await new Promise<void>((resolve, reject) => {
    video.onloadeddata = () => resolve();
    video.onerror = () => reject(new Error('fixture video failed to load'));
  });

  const snap = document.createElement('canvas');
  snap.width = WIDTH;
  snap.height = HEIGHT;
  const snapCtx = snap.getContext('2d')!;

  const frames: string[] = [];
  for (const t of DECODE_TIMES_S) {
    await new Promise<void>((resolve) => {
      video.onseeked = () => resolve();
      video.currentTime = Math.min(t, Math.max(video.duration - 0.05, 0));
    });
    snapCtx.drawImage(video, 0, 0, WIDTH, HEIGHT);
    frames.push(snap.toDataURL('image/png'));
  }
  URL.revokeObjectURL(video.src);
  return frames;
}

/** Draw + blur + record + decode once. `blurRects` empty means "no blur applied". */
async function recordAndDecode(text: string, blurRects: RectLike[]): Promise<string[]> {
  const source = document.createElement('canvas');
  source.width = WIDTH;
  source.height = HEIGHT;
  drawSource(source, text);

  const output = document.createElement('canvas');
  output.width = WIDTH;
  output.height = HEIGHT;
  const ctx = output.getContext('2d')!;

  const resolved = validateRegions(blurRects);
  if (resolved === null) throw new Error('fixture blur regions failed to validate');
  compositeFrame(ctx, source, WIDTH, HEIGHT, resolved);

  const blob = await recordWebm(output);
  return decodeFrames(blob);
}

async function run(): Promise<BlurFixtureResult> {
  const phiText = 'MRN-9384710-NIGHTLY';
  const blurredFrames = await recordAndDecode(phiText, [TEXT_RECT]);
  const referenceFrames = await recordAndDecode(phiText, []);
  return { blurredFrames, referenceFrame: referenceFrames[0]!, phiText };
}

const fixture: BlurFixture = { run };
window.__htrBlurFixture = fixture;
