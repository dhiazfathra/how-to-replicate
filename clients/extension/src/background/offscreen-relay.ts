import { newId, type CaptureEvent, type Clock, type InstantReplay } from '@htr/capture-core';
import { decodeChunk } from '../lib/chunk-codec.js';
import type { ChromeTabs } from '../lib/chrome-adapter.js';

export type OffscreenMessage =
  // `data` crosses `chrome.runtime.sendMessage` as base64 — see `chunk-codec.ts`
  // for why a raw `Uint8Array`/`Blob` can't survive that trip.
  | { type: 'htr:video-chunk'; captureId: string; data: string }
  | { type: 'htr:screenshot'; captureId: string; dataUrl: string }
  | { type: 'htr:lifecycle'; captureId: string; transition: string; detail: string | null }
  | { type: 'htr:degraded'; captureId: string };

const MESSAGE_TYPES: ReadonlySet<OffscreenMessage['type']> = new Set([
  'htr:video-chunk',
  'htr:screenshot',
  'htr:lifecycle',
  'htr:degraded',
]);

/** The offscreen document's `chrome.tabs.captureVisibleTab()` request — see `createScreenshotRequestListener`. */
export type ScreenshotRequestMessage = { type: 'htr:capture-screenshot-request'; captureId: string };

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
        buffer.pushVideoChunk(decodeChunk(message.data));
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

function isScreenshotRequestMessage(message: unknown): message is ScreenshotRequestMessage {
  if (typeof message !== 'object' || message === null) return false;
  return (message as { type?: unknown }).type === 'htr:capture-screenshot-request';
}

/**
 * Offscreen documents cannot call `chrome.tabs.*` (MV3 restricts them to
 * `chrome.runtime` plus a small allowlist) — only the service worker can.
 * This is the other half of that seam: it answers the offscreen document's
 * `htr:capture-screenshot-request` by calling `chrome.tabs.captureVisibleTab()`
 * here and returning the result through `sendResponse`, keeping the message
 * channel open (`return true`) until the async capture resolves.
 */
export function createScreenshotRequestListener(
  captureId: string,
  tabs: Pick<ChromeTabs, 'captureVisibleTab'>,
): (message: unknown, sender: unknown, sendResponse: (r?: unknown) => void) => boolean | void {
  return (message, _sender, sendResponse): boolean | void => {
    if (!isScreenshotRequestMessage(message) || message.captureId !== captureId) return;
    void tabs.captureVisibleTab().then((dataUrl) => sendResponse(dataUrl));
    return true;
  };
}
