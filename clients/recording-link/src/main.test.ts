import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * `capture-flow.test.ts` only spies on `fetch` around `finalizeRecording` —
 * it never exercises `main.ts`, which is the module that actually talks to
 * `getDisplayMedia`/`MediaRecorder`/the DOM and is the one place a future
 * change could accidentally wire in a network call during recording (not
 * just at finalize). This test drives the full page lifecycle — start,
 * record a chunk, stop, finalize, download — through `main.ts` itself, with
 * a `fetch` spy live the entire time, so an accidental egress call anywhere
 * in the recording-link flow (not only inside `finalizeRecording`) fails a
 * test. `main.ts` is excluded from the coverage gate (see
 * `vitest.config.ts`) because it's pure wiring against globals jsdom
 * doesn't provide — this test is the regression guard that gap needs.
 */
function spyOnFetch() {
  return vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('fetch must not be called'));
}

describe('main.ts recording-link wiring', () => {
  let fetchSpy: ReturnType<typeof spyOnFetch>;
  let createObjectURLSpy: ReturnType<typeof vi.fn>;
  let revokeObjectURLSpy: ReturnType<typeof vi.fn>;
  type FakeRecorder = {
    state: string;
    ondataavailable: ((event: { data: Uint8Array }) => void) | null;
  };
  let recorderInstance: FakeRecorder | undefined;

  function FakeMediaRecorder(): FakeRecorder {
    const recorder: FakeRecorder = { state: 'inactive', ondataavailable: null };
    Object.assign(recorder, {
      start: () => void (recorder.state = 'recording'),
      stop: () => {
        recorder.state = 'inactive';
        recorder.ondataavailable?.({ data: new Uint8Array([1, 2, 3]) });
      },
    });
    recorderInstance = recorder;
    return recorder;
  }

  beforeEach(() => {
    recorderInstance = undefined;
    document.body.innerHTML = `
      <button id="start" type="button">Start</button>
      <button id="stop" type="button" disabled>Stop</button>
      <p id="status" role="status"></p>
      <a id="download" download="capture.htr" hidden>Download</a>
    `;

    fetchSpy = spyOnFetch();

    const track = { stop: vi.fn() };
    const displayStream = { getTracks: () => [track] };
    vi.stubGlobal('navigator', {
      ...navigator,
      mediaDevices: { getDisplayMedia: vi.fn().mockResolvedValue(displayStream) },
      userAgent: 'test-agent',
      platform: 'test',
      language: 'en-US',
    });

    vi.stubGlobal('MediaRecorder', FakeMediaRecorder);
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
      filter: 'none',
      drawImage: vi.fn(),
      save: vi.fn(),
      restore: vi.fn(),
      beginPath: vi.fn(),
      rect: vi.fn(),
      clip: vi.fn(),
    } as unknown as CanvasRenderingContext2D);
    (HTMLCanvasElement.prototype as unknown as { captureStream: () => unknown }).captureStream = () => 'canvas-stream';

    createObjectURLSpy = vi.fn(() => 'blob:fake-url');
    revokeObjectURLSpy = vi.fn();
    vi.stubGlobal('URL', { ...URL, createObjectURL: createObjectURLSpy, revokeObjectURL: revokeObjectURLSpy });

    vi.stubGlobal('requestAnimationFrame', vi.fn());
  });

  afterEach(() => {
    fetchSpy.mockRestore();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it('runs start -> record -> stop -> finalize -> download without ever calling fetch', async () => {
    await import('./main.js');

    const startButton = document.getElementById('start') as HTMLButtonElement;
    const stopButton = document.getElementById('stop') as HTMLButtonElement;
    const downloadLink = document.getElementById('download') as HTMLAnchorElement;

    startButton.click();
    // Flush the microtask queue for the async `start()` handler.
    await vi.waitFor(() => expect(stopButton.disabled).toBe(false));

    stopButton.click();
    await vi.waitFor(() => expect(downloadLink.hidden).toBe(false));

    expect(downloadLink.href).toContain('blob:fake-url');
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(recorderInstance).toBeDefined();
  });

  it('revokes the previous download URL when a second recording finalizes', async () => {
    await import('./main.js');

    const startButton = document.getElementById('start') as HTMLButtonElement;
    const stopButton = document.getElementById('stop') as HTMLButtonElement;
    const downloadLink = document.getElementById('download') as HTMLAnchorElement;

    startButton.click();
    await vi.waitFor(() => expect(stopButton.disabled).toBe(false));
    stopButton.click();
    await vi.waitFor(() => expect(downloadLink.hidden).toBe(false));

    createObjectURLSpy.mockReturnValue('blob:second-url');
    downloadLink.hidden = true;
    startButton.click();
    await vi.waitFor(() => expect(stopButton.disabled).toBe(false));
    stopButton.click();
    await vi.waitFor(() => expect(downloadLink.hidden).toBe(false));
    expect(downloadLink.href).toContain('blob:second-url');

    expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:fake-url');
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
