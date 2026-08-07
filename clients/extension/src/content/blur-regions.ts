import type { RectLike } from '../lib/dom-types.js';

/**
 * Message-passing contract with the offscreen recorder document (Task 12
 * implements the receiving end — this module only defines and sends the
 * message, it never touches `MediaRecorder` or canvas compositing itself).
 */
export const BLUR_REGIONS_MESSAGE_TYPE = 'htr:blur-regions';

export type BlurRegionsMessage = {
  type: typeof BLUR_REGIONS_MESSAGE_TYPE;
  captureId: string;
  regions: RectLike[];
};

export type QueryableDocument = {
  querySelectorAll(selector: string): { getBoundingClientRect(): RectLike }[];
};

export type FrameScheduler = {
  requestFrame(callback: () => void): void;
};

/**
 * Resolve every `video-blur` selector to its current screen rect. Re-run per
 * frame (via the injected `FrameScheduler`) so a rect tracks scroll and
 * layout changes rather than going stale mid-recording — a blur region that
 * lags behind a scrolled element is an unblurred frame in every practical
 * sense (invariant 2).
 */
export function resolveBlurRegions(doc: QueryableDocument, selectors: string[]): RectLike[] {
  const regions: RectLike[] = [];
  for (const selector of selectors) {
    for (const el of doc.querySelectorAll(selector)) {
      regions.push(el.getBoundingClientRect());
    }
  }
  return regions;
}

export type RuntimePoster = {
  sendMessage(message: BlurRegionsMessage): void;
};

/**
 * Start posting resolved blur regions to the offscreen document every frame.
 * Returns a teardown function. Posting happens unconditionally — even zero
 * regions is a meaningful frame (nothing to blur right now), so the offscreen
 * recorder always has a current answer rather than a stale one.
 */
export function startBlurRegionTracking(
  doc: QueryableDocument,
  runtime: RuntimePoster,
  scheduler: FrameScheduler,
  captureId: string,
  selectors: string[],
): () => void {
  let stopped = false;

  const tick = (): void => {
    if (stopped) return;
    runtime.sendMessage({
      type: BLUR_REGIONS_MESSAGE_TYPE,
      captureId,
      regions: resolveBlurRegions(doc, selectors),
    });
    scheduler.requestFrame(tick);
  };

  scheduler.requestFrame(tick);

  return (): void => {
    stopped = true;
  };
}
