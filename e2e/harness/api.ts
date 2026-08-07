import type { Capture, CaptureEvent, RedactionRuleset } from '@htr/capture-core';

/**
 * The surface `e2e/harness/entry.ts` publishes on `window.__htr` inside the
 * demo page. Types only — imported by both the in-page harness and the
 * Node-side specs, so neither one drifts from the other.
 *
 * Everything behind these methods is real product code from
 * `packages/capture-core` and `clients/extension`; the harness itself only
 * plays the part the MV3 service worker plays in production (owning the
 * buffer, holding the clock, driving the state machine).
 */
export type Harness = {
  /** The ruleset this harness redacts with. Built from capture-core's `PHI_PATTERNS`. */
  ruleset(): RedactionRuleset;
  /** Mint a capture in `recording` and start the real interaction trail on `document`. */
  start(): string;
  /**
   * Feed one raw CDP message through the extension's real `mapCdpEvent`, then
   * into the real `InstantReplay` (which redacts on ingest). Node forwards
   * these from a real `CDPSession` attached to this page.
   */
  pushCdp(message: { method: string; params: Record<string, unknown> }): void;
  /** Redacted events currently held by the live buffer, before finalize. */
  events(): CaptureEvent[];
  /** Counts held by the live buffer, before finalize. */
  stats(): { events: number; withheld: number };
  /** recording -> redacting -> composing -> ready, then persist to IndexedDB. */
  stop(): Promise<Capture>;
  /** Persist an arbitrary capture (used to seed the invariant-1 fail-closed case). */
  seedCapture(overrides: Partial<Capture>): Promise<Capture>;
  listCaptures(): Promise<Capture[]>;
  readEvents(captureId: string): Promise<CaptureEvent[]>;
};

declare global {
  interface Window {
    __htr: Harness;
  }
}
