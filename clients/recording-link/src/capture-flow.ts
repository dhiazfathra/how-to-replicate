import {
  buildHtrBundle,
  compositeFrame,
  createInstantReplay,
  createRedactor,
  createRingBuffer,
  newId,
  transition,
  generateDoc,
  type AssetRef,
  type Capture,
  type CaptureEvent,
  type Clock,
  type DrawTarget,
  type EnvSnapshot,
  type RedactionRuleset,
} from '@htr/capture-core';

/**
 * Structural subset of `OffscreenCanvas`/`HTMLCanvasElement` this module
 * draws to and records from — same seam as the extension's offscreen
 * recorder, kept separate per-client rather than shared: this client has no
 * `chrome.offscreen` document, draws from a `<video>` element playing the
 * `getDisplayMedia` stream directly (not a relayed frame source), and never
 * tracks live blur regions (there is no content script here to report DOM
 * selector positions — see the header note in `main.ts`).
 */
export type CanvasTarget = {
  getContext(kind: '2d'): DrawTarget;
  captureStream(): unknown;
};

export type FrameSource = {
  width: number;
  height: number;
  frame: unknown;
};

export type Scheduler = {
  requestFrame(callback: () => void): void;
};

export type RecorderChunk = Blob | Uint8Array;

export type MediaRecorderLike = {
  state: string;
  start(timesliceMs?: number): void;
  stop(): void;
  ondataavailable: ((event: { data: RecorderChunk }) => void) | null;
};

export type MediaRecorderFactory = (canvasStream: unknown) => MediaRecorderLike;

export type RecordingHandle = {
  stop(): void;
};

export type StartRecordingOptions = {
  source: FrameSource;
  canvas: CanvasTarget;
  createMediaRecorder: MediaRecorderFactory;
  scheduler: Scheduler;
  onChunk: (chunk: RecorderChunk) => void;
};

/**
 * Draw + blur (Task 12's `compositeFrame`, no regions — see the `main.ts`
 * header note on why this client never has selector-based regions) each
 * frame onto `canvas`, and feed **the canvas's own stream** to
 * `MediaRecorder`. The display stream backing `source.frame` is only ever
 * read as a `drawImage` source — it is never passed to
 * `createMediaRecorder` — so an unblurred frame can never reach the
 * persisted artifact (invariant 2), even though today's region list is
 * always empty.
 */
export function startRecording(options: StartRecordingOptions): RecordingHandle {
  const { source, canvas, createMediaRecorder, scheduler, onChunk } = options;
  const ctx = canvas.getContext('2d');
  const recorder = createMediaRecorder(canvas.captureStream());
  recorder.ondataavailable = (event): void => {
    if (event.data) onChunk(event.data);
  };
  recorder.start(1000);

  let stopped = false;
  const tick = (): void => {
    if (stopped) return;
    compositeFrame(ctx, source.frame, source.width, source.height, []);
    scheduler.requestFrame(tick);
  };
  scheduler.requestFrame(tick);

  return {
    stop(): void {
      if (stopped) return;
      stopped = true;
      recorder.stop();
    },
  };
}

async function chunksToBytes(chunks: readonly RecorderChunk[]): Promise<Uint8Array> {
  const buffers = await Promise.all(
    chunks.map(async (chunk) => (chunk instanceof Uint8Array ? chunk : new Uint8Array(await chunk.arrayBuffer()))),
  );
  const total = buffers.reduce((sum, b) => sum + b.byteLength, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const buffer of buffers) {
    out.set(buffer, offset);
    offset += buffer.byteLength;
  }
  return out;
}

async function sha256Hex(bytes: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', bytes as BufferSource);
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, '0')).join('');
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Raised when finalizing a recording fails after it left `recording` — carries the resulting `failed` capture. */
export class RecordingFinalizeError extends Error {
  readonly capture: Capture;

  constructor(message: string, capture: Capture) {
    super(message);
    this.name = 'RecordingFinalizeError';
    this.capture = capture;
  }
}

export type FinalizeRecordingOptions = {
  ruleset: RedactionRuleset;
  chunks: readonly RecorderChunk[];
  env: EnvSnapshot;
  clock: Clock;
  createdAt?: string;
  idFactory?: () => string;
};

export type FinalizeRecordingResult = {
  bundle: Uint8Array;
  capture: Capture;
};

/**
 * Run a finished recording through the same `capture-core` redaction
 * pipeline and `.htr` builder the extension uses: mint a capture, walk it
 * `recording -> redacting -> composing -> ready` (or `-> failed` on error,
 * never leaving it viewable), redact the lifecycle timeline through the
 * bundled ruleset, and build the export bundle. `buildHtrBundle` gates on
 * `state === 'ready'` (invariant 1), so a thrown `RecordingFinalizeError`
 * always carries a `failed` capture — there is no path that hands back
 * export bytes for anything but a `ready` capture.
 */
export async function finalizeRecording(options: FinalizeRecordingOptions): Promise<FinalizeRecordingResult> {
  const { ruleset, chunks, env, clock } = options;
  const idFactory = options.idFactory ?? newId;
  const createdAt = options.createdAt ?? new Date().toISOString();

  let capture: Capture = {
    id: idFactory(),
    workspaceId: null,
    projectId: null,
    source: 'recording-link',
    state: 'recording',
    fidelity: 'full',
    createdAt,
    epoch: clock.epoch,
    env,
    metadata: { rulesetVersion: ruleset.version },
    doc: null,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
  };

  const redactor = createRedactor(ruleset);
  const replay = createInstantReplay({ redactor, ring: createRingBuffer<CaptureEvent>(), clock });

  const toRedacting = transition(capture, 'redacting', null, clock.now());
  capture = toRedacting.capture;
  replay.ingest(toRedacting.event);

  const toComposing = transition(capture, 'composing', null, clock.now());
  capture = toComposing.capture;
  replay.ingest(toComposing.event);

  try {
    const videoBytes = await chunksToBytes(chunks);
    const asset: AssetRef = {
      id: idFactory(),
      captureId: capture.id,
      kind: 'video',
      mimeType: 'video/webm',
      sizeBytes: videoBytes.byteLength,
      chunkCount: chunks.length,
      sha256: await sha256Hex(videoBytes),
    };

    const events = replay.events();
    const withheldEventCount = replay.withheldEventCount();
    const doc = generateDoc(events, { assets: [asset] });

    const composed: Capture = {
      ...capture,
      doc,
      assets: [asset],
      withheldEventCount,
      fidelity: withheldEventCount > 0 ? 'degraded' : capture.fidelity,
    };

    const toReady = transition(composed, 'ready', null, clock.now());
    const finalEvents = [...events, toReady.event];
    const bundle = buildHtrBundle(toReady.capture, finalEvents, [{ ref: asset, bytes: videoBytes }]);

    return { bundle, capture: toReady.capture };
  } catch (error) {
    const toFailed = transition(capture, 'failed', errorMessage(error), clock.now());
    throw new RecordingFinalizeError(errorMessage(error), toFailed.capture);
  }
}
