/** Fixed-duration chunk length `MediaRecorder` is asked to flush at. */
export const TIMESLICE_MS = 1000;

/** Structural subset of `MediaRecorder` this module drives. */
export type MediaRecorderLike = {
  state: 'inactive' | 'recording' | 'paused';
  start(timesliceMs?: number): void;
  stop(): void;
  ondataavailable: ((event: { data: Uint8Array | Blob }) => void) | null;
  /** Fires after the final `ondataavailable` for pending data — the point at which stop() has actually flushed. */
  onstop: (() => void) | null;
};

export type ChunkSink = {
  pushVideoChunk(data: Uint8Array | Blob): void;
};

/**
 * Start `recorder` in timeslice mode, forwarding every emitted chunk into
 * the Task 6 bounded deque (`ChunkSink`, satisfied by `InstantReplay`).
 */
export function startTimesliceRecording(
  recorder: MediaRecorderLike,
  sink: ChunkSink,
  timesliceMs: number = TIMESLICE_MS,
): void {
  recorder.ondataavailable = (event): void => {
    sink.pushVideoChunk(event.data);
  };
  recorder.start(timesliceMs);
}

/**
 * Idempotent stop — safe to call on an already-inactive recorder. Resolves
 * only once `onstop` fires, i.e. once the recorder's final pending chunk has
 * already reached `ondataavailable` — callers that gate teardown on this
 * promise can no longer race the last chunk.
 */
export function stopTimesliceRecording(recorder: MediaRecorderLike): Promise<void> {
  if (recorder.state === 'inactive') return Promise.resolve();
  return new Promise((resolve) => {
    recorder.onstop = () => resolve();
    recorder.stop();
  });
}
