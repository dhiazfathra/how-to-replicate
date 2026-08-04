import type { Clock } from '@htr/capture-core';

/**
 * Service-worker -> content-script session control (Critical 3/4 fix). The
 * content script cannot know its capture id, epoch, or blur selectors on its
 * own — `service-worker.ts`'s `start()` pushes them here over
 * `chrome.tabs.sendMessage`, and `stop()` pushes the matching stop message.
 * Everything the content script does (interaction trail, blur-region rAF
 * loop) is gated on having received a start message, per the "no unconditional
 * rAF loop" fix — a page with no active capture runs neither.
 */
export const CAPTURE_START_MESSAGE_TYPE = 'htr:capture-start';
export const CAPTURE_STOP_MESSAGE_TYPE = 'htr:capture-stop';

export type CaptureStartMessage = {
  type: typeof CAPTURE_START_MESSAGE_TYPE;
  captureId: string;
  epoch: number;
  blurSelectors: string[];
};

export type CaptureStopMessage = {
  type: typeof CAPTURE_STOP_MESSAGE_TYPE;
  captureId: string;
};

export type SessionHandlers = {
  onStart(message: CaptureStartMessage): void;
  onStop(message: CaptureStopMessage): void;
};

/** Build the `chrome.runtime.onMessage` handler wiring session control into `handlers`. */
export function createSessionListener(handlers: SessionHandlers): (message: unknown) => void {
  return (message: unknown): void => {
    if (isCaptureStartMessage(message)) {
      handlers.onStart(message);
      return;
    }
    if (isCaptureStopMessage(message)) {
      handlers.onStop(message);
    }
  };
}

function isCaptureStartMessage(message: unknown): message is CaptureStartMessage {
  if (typeof message !== 'object' || message === null) return false;
  const candidate = message as Partial<CaptureStartMessage>;
  return (
    candidate.type === CAPTURE_START_MESSAGE_TYPE &&
    typeof candidate.captureId === 'string' &&
    typeof candidate.epoch === 'number' &&
    Array.isArray(candidate.blurSelectors)
  );
}

function isCaptureStopMessage(message: unknown): message is CaptureStopMessage {
  return (
    typeof message === 'object' &&
    message !== null &&
    (message as { type?: unknown }).type === CAPTURE_STOP_MESSAGE_TYPE
  );
}

export type PerformanceLike = {
  timeOrigin: number;
  now(): number;
};

/**
 * Anchor a `Clock` to a capture's shared `epoch` (minted by the service
 * worker's own `createClock()`) instead of minting a second, independent
 * epoch in the content-script context. `timeOrigin` is wall-clock-based in
 * every context, so this offset is directly comparable to the service
 * worker's own `clock.now()` calls for the same capture (Critical 4 fix).
 */
export function createAnchoredClock(epoch: number, perf: PerformanceLike = performance): Clock {
  return {
    epoch,
    now(): number {
      const offset = perf.timeOrigin + perf.now() - epoch;
      return offset < 0 ? 0 : offset;
    },
  };
}
