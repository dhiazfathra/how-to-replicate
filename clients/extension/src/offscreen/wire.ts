import { BLUR_REGIONS_MESSAGE_TYPE, type BlurRegionsMessage } from '../content/blur-regions.js';
import type { RectLike } from '../lib/dom-types.js';
import type { FrameSource } from './recorder.js';

export type RegionsHandle = {
  updateRegions(regions: readonly RectLike[]): void;
};

/**
 * Build the `chrome.runtime.onMessage` handler that feeds Task 11's
 * per-frame `BlurRegionsMessage`s into the recorder. Ignores anything that
 * isn't a well-formed message for this exact capture — cross-tab/cross-capture
 * noise on the shared `runtime.onMessage` channel must never overwrite the
 * regions this recorder is compositing.
 */
export function createBlurRegionsListener(
  captureId: string,
  handle: RegionsHandle,
): (message: unknown) => void {
  return (message: unknown): void => {
    if (!isBlurRegionsMessage(message) || message.captureId !== captureId) return;
    handle.updateRegions(message.regions);
  };
}

function isBlurRegionsMessage(message: unknown): message is BlurRegionsMessage {
  return (
    typeof message === 'object' &&
    message !== null &&
    (message as { type?: unknown }).type === BLUR_REGIONS_MESSAGE_TYPE
  );
}

/** Structural subset of `HTMLVideoElement` this module needs to host the display stream. */
export type VideoElementLike = {
  srcObject: unknown;
  videoWidth: number;
  videoHeight: number;
  play(): Promise<void>;
};

/**
 * Adapt a `getDisplayMedia()` stream into the `FrameSource` `recorder.ts`
 * expects: play it into a hidden `<video>` element and read dimensions/frame
 * live off that element on every access, so a resize mid-capture is picked
 * up on the very next composite rather than baked in at start time.
 */
export function createFrameSource(video: VideoElementLike, stream: unknown): FrameSource {
  video.srcObject = stream;
  void video.play();
  return {
    get width(): number {
      return video.videoWidth;
    },
    get height(): number {
      return video.videoHeight;
    },
    get frame(): unknown {
      return video;
    },
  };
}
