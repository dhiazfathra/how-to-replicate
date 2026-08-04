import { newId, type CaptureEvent, type Clock, type InstantReplay } from '@htr/capture-core';

export type OffscreenMessage =
  | { type: 'htr:video-chunk'; captureId: string; data: Uint8Array | Blob }
  | { type: 'htr:screenshot'; captureId: string; dataUrl: string }
  | { type: 'htr:lifecycle'; captureId: string; transition: string; detail: string | null }
  | { type: 'htr:degraded'; captureId: string };

const MESSAGE_TYPES: ReadonlySet<OffscreenMessage['type']> = new Set([
  'htr:video-chunk',
  'htr:screenshot',
  'htr:lifecycle',
  'htr:degraded',
]);

/**
 * Build the `chrome.runtime.onMessage` handler that routes Task 12's
 * offscreen-recorder signals (`main.ts`'s `DegradeSink`, relayed over
 * `runtime.sendMessage` since the offscreen document doesn't hold the
 * `InstantReplay` buffer itself) into this capture's buffer and fidelity
 * state. Same fail-closed filter as `wire.ts`'s `createBlurRegionsListener`:
 * anything not addressed to this exact `captureId` is ignored outright, so a
 * stray message from another capture's offscreen document can never be
 * misattributed here.
 */
export function createOffscreenRelayListener(
  captureId: string,
  buffer: InstantReplay,
  clock: Clock,
  markDegraded: () => void,
): (message: unknown) => void {
  return (message: unknown): void => {
    if (!isOffscreenMessage(message) || message.captureId !== captureId) return;

    switch (message.type) {
      case 'htr:video-chunk':
        buffer.pushVideoChunk(message.data);
        return;
      case 'htr:screenshot':
        buffer.pushScreenshot(message.dataUrl);
        return;
      case 'htr:lifecycle':
        buffer.ingest(buildLifecycleEvent(captureId, clock, message.transition, message.detail));
        return;
      case 'htr:degraded':
        markDegraded();
        return;
    }
  };
}

function buildLifecycleEvent(
  captureId: string,
  clock: Clock,
  transition: string,
  detail: string | null,
): CaptureEvent {
  return {
    id: newId(),
    captureId,
    t: clock.now(),
    kind: 'lifecycle',
    payload: { transition, detail },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function isOffscreenMessage(message: unknown): message is OffscreenMessage {
  if (typeof message !== 'object' || message === null) return false;
  const type = (message as { type?: unknown }).type;
  return typeof type === 'string' && MESSAGE_TYPES.has(type as OffscreenMessage['type']);
}
