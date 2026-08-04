import { describe, expect, it, vi } from 'vitest';
import { createCaptureController } from './capture-controller.js';
import type { ActiveCapture, ServiceWorker } from './service-worker.js';

function fakeActive(id: string): ActiveCapture {
  // Only `capture.id` is ever read by the controller under test; the other
  // `ActiveCapture` fields are opaque to it and irrelevant here.
  return { capture: { id } } as unknown as ActiveCapture;
}

function fakeWorker(overrides: Partial<ServiceWorker> = {}): ServiceWorker {
  return {
    rulesetStore: {},
    start: vi.fn(),
    stop: vi.fn(),
    ensureOffscreenDocument: vi.fn(),
    ...overrides,
  } as ServiceWorker;
}

describe('createCaptureController', () => {
  it('starts a capture on click when none is active', async () => {
    const started = fakeActive('cap-1');
    const start = vi.fn().mockResolvedValue(started);
    const worker = fakeWorker({ start });
    const controller = createCaptureController(worker);

    await controller.onClicked({ id: 7, url: 'https://example.test/page' });

    expect(start).toHaveBeenCalledWith('https://example.test', { tabId: 7 });
  });

  it('ignores the click if the tab has no id', async () => {
    const start = vi.fn();
    const controller = createCaptureController(fakeWorker({ start }));

    await controller.onClicked({ url: 'https://example.test' });

    expect(start).not.toHaveBeenCalled();
  });

  it('ignores the click if the tab has no url', async () => {
    const start = vi.fn();
    const controller = createCaptureController(fakeWorker({ start }));

    await controller.onClicked({ id: 1 });

    expect(start).not.toHaveBeenCalled();
  });

  it('stops the active capture on a second click', async () => {
    const started = fakeActive('cap-1');
    const start = vi.fn().mockResolvedValue(started);
    const stop = vi.fn().mockResolvedValue(started.capture);
    const controller = createCaptureController(fakeWorker({ start, stop }));

    await controller.onClicked({ id: 7, url: 'https://example.test' });
    await controller.onClicked({ id: 7, url: 'https://example.test' });

    expect(stop).toHaveBeenCalledWith(started);
  });

  it('allows starting a new capture after the previous one was stopped', async () => {
    const first = fakeActive('cap-1');
    const second = fakeActive('cap-2');
    const start = vi.fn().mockResolvedValueOnce(first).mockResolvedValueOnce(second);
    const stop = vi.fn().mockResolvedValue(first.capture);
    const controller = createCaptureController(fakeWorker({ start, stop }));

    await controller.onClicked({ id: 7, url: 'https://example.test' });
    await controller.onClicked({ id: 7, url: 'https://example.test' });
    await controller.onClicked({ id: 7, url: 'https://example.test' });

    expect(start).toHaveBeenCalledTimes(2);
  });

  it('ignores a click that arrives while a start is already in flight (race guard)', async () => {
    let resolveStart!: (v: ActiveCapture) => void;
    const start = vi.fn().mockReturnValue(
      new Promise<ActiveCapture>((resolve) => {
        resolveStart = resolve;
      }),
    );
    const stop = vi.fn();
    const controller = createCaptureController(fakeWorker({ start, stop }));

    const firstClick = controller.onClicked({ id: 7, url: 'https://example.test' });
    // Second click arrives before the first start() resolves.
    await controller.onClicked({ id: 7, url: 'https://example.test' });

    expect(start).toHaveBeenCalledTimes(1);
    expect(stop).not.toHaveBeenCalled();

    resolveStart(fakeActive('cap-1'));
    await firstClick;
  });

  it('ignores a click that arrives while a stop is already in flight (race guard)', async () => {
    const started = fakeActive('cap-1');
    const start = vi.fn().mockResolvedValue(started);
    let resolveStop!: () => void;
    const stop = vi.fn().mockReturnValue(
      new Promise<void>((resolve) => {
        resolveStop = resolve;
      }),
    );
    const controller = createCaptureController(fakeWorker({ start, stop }));

    await controller.onClicked({ id: 7, url: 'https://example.test' });
    const stopClick = controller.onClicked({ id: 7, url: 'https://example.test' });
    // Third click arrives while stop() is still in flight.
    await controller.onClicked({ id: 7, url: 'https://example.test' });

    expect(stop).toHaveBeenCalledTimes(1);
    expect(start).toHaveBeenCalledTimes(1);

    resolveStop();
    await stopClick;
  });
});
