import { describe, expect, it, vi } from 'vitest';
import { startTimesliceRecording, stopTimesliceRecording, TIMESLICE_MS, type MediaRecorderLike } from './timeslice.js';

function fakeRecorder(state: MediaRecorderLike['state'] = 'inactive'): MediaRecorderLike & {
  startArgs: (number | undefined)[];
  stopCalls: number;
} {
  const startArgs: (number | undefined)[] = [];
  let stopCalls = 0;
  const recorder: MediaRecorderLike & { startArgs: (number | undefined)[]; stopCalls: number } = {
    state,
    ondataavailable: null,
    onstop: null,
    startArgs,
    get stopCalls() {
      return stopCalls;
    },
    start(timesliceMs?: number): void {
      startArgs.push(timesliceMs);
    },
    stop(): void {
      stopCalls += 1;
      recorder.state = 'inactive';
      recorder.onstop?.();
    },
  };
  return recorder;
}

describe('startTimesliceRecording', () => {
  it('starts the recorder with the default timeslice and forwards chunks to the sink', () => {
    const recorder = fakeRecorder();
    const sink = { pushVideoChunk: vi.fn() };

    startTimesliceRecording(recorder, sink);

    expect(recorder.startArgs).toEqual([TIMESLICE_MS]);
    const chunk = new Uint8Array([1]);
    recorder.ondataavailable?.({ data: chunk });
    expect(sink.pushVideoChunk).toHaveBeenCalledWith(chunk);
  });

  it('honors a custom timeslice', () => {
    const recorder = fakeRecorder();
    startTimesliceRecording(recorder, { pushVideoChunk: vi.fn() }, 250);
    expect(recorder.startArgs).toEqual([250]);
  });
});

describe('stopTimesliceRecording', () => {
  it('stops a recording recorder and resolves once onstop fires', async () => {
    const recorder = fakeRecorder('recording');
    await stopTimesliceRecording(recorder);
    expect(recorder.stopCalls).toBe(1);
  });

  it('is a no-op on an already-inactive recorder', async () => {
    const recorder = fakeRecorder('inactive');
    await stopTimesliceRecording(recorder);
    expect(recorder.stopCalls).toBe(0);
  });
});
