/** Fixed-duration chunk length `MediaRecorder` is asked to flush at. */
export const TIMESLICE_MS = 1000;

/** Structural subset of `MediaRecorder` this module drives. */
export type MediaRecorderLike = {
  state: 'inactive' | 'recording' | 'paused';
  start(timesliceMs?: number): void;
  stop(): void;
  ondataavailable: ((event: { data: Uint8Array | Blob }) => void) | null;
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

/** Idempotent stop — safe to call on an already-inactive recorder. */
export function stopTimesliceRecording(recorder: MediaRecorderLike): void {
  if (recorder.state !== 'inactive') recorder.stop();
}
