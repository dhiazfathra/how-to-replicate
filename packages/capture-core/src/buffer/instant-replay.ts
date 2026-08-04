import type { Redactor } from '../redaction/engine.js';
import type { CaptureEvent } from '../types/event.js';
import type { RingBuffer } from './ring.js';

export type ReplayClock = {
  now(): number;
};

export type VideoChunk = {
  data: Uint8Array | Blob;
  t: number;
};

export type Screenshot = {
  dataUrl: string;
  t: number;
};

export type InstantReplayOptions = {
  redactor: Redactor;
  ring: RingBuffer<CaptureEvent>;
  clock: ReplayClock;
  /** Video-chunk retention window in ms. Defaults to 120 seconds. */
  windowMs?: number;
};

export type InstantReplay = {
  /** Redacts on ingest, then pushes: the buffer never holds raw PHI. */
  ingest(rawEvent: CaptureEvent): void;
  /** Redaction-caused drops only (invariant 4: lost fidelity must be visible). */
  withheldEventCount(): number;
  /** Ring-buffer capacity evictions. Dropped real events, so pipeline/run.ts folds this into fidelity/withheldEventCount (invariant 4) same as redaction drops. */
  evictedCount(): number;
  events(): CaptureEvent[];
  pushVideoChunk(data: Uint8Array | Blob): void;
  videoChunks(): VideoChunk[];
  /** Degraded-mode visual-evidence floor (invariant 2's "screenshot-only" path). Same window as video chunks. */
  pushScreenshot(dataUrl: string): void;
  screenshots(): Screenshot[];
};

export const VIDEO_WINDOW_MS = 120_000;

/**
 * Instant Replay: an in-memory rolling buffer of already-redacted events plus
 * a bounded window of raw video chunks. Redaction happens at ingest, not at
 * finalize, so unredacted PHI never has a resting place in memory.
 */
export function createInstantReplay(options: InstantReplayOptions): InstantReplay {
  const { redactor, ring, clock } = options;
  const windowMs = options.windowMs ?? VIDEO_WINDOW_MS;

  let withheld = 0;
  let evicted = 0;
  const videoChunks: VideoChunk[] = [];
  const screenshots: Screenshot[] = [];

  function releaseExpired<T extends { t: number }>(items: T[]): void {
    const cutoff = clock.now() - windowMs;
    while (items.length > 0 && items[0]!.t < cutoff) {
      items.shift();
    }
  }

  return {
    ingest(rawEvent: CaptureEvent): void {
      const outcome = redactor.redactEvent(rawEvent);
      if (outcome.fidelity === 'dropped') {
        withheld += 1;
        return;
      }
      const droppedByEviction = ring.push(outcome.event);
      evicted += droppedByEviction.length;
    },

    withheldEventCount(): number {
      return withheld;
    },

    evictedCount(): number {
      return evicted;
    },

    events(): CaptureEvent[] {
      return ring.toArray();
    },

    pushVideoChunk(data: Uint8Array | Blob): void {
      videoChunks.push({ data, t: clock.now() });
      releaseExpired(videoChunks);
    },

    videoChunks(): VideoChunk[] {
      releaseExpired(videoChunks);
      return [...videoChunks];
    },

    pushScreenshot(dataUrl: string): void {
      screenshots.push({ dataUrl, t: clock.now() });
      releaseExpired(screenshots);
    },

    screenshots(): Screenshot[] {
      releaseExpired(screenshots);
      return [...screenshots];
    },
  };
}
