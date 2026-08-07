/**
 * The surface `entry.ts` publishes on `window.__htrBlurFixture`. Types only
 * — imported by both the in-page fixture and the Node-side spec so neither
 * drifts from the other, same convention as `e2e/harness/api.ts`.
 */
export type BlurFixtureResult = {
  /** PNG data URLs decoded from a *blurred* recording — what must not OCR. */
  blurredFrames: string[];
  /**
   * A PNG data URL decoded from the exact same recording pipeline with the
   * blur region disabled. Proves the fixture text is actually OCR-legible in
   * the first place, so a passing blurred-frame assertion isn't vacuous.
   */
  referenceFrame: string;
  /** The synthetic PHI string drawn into the fixture, and asserted absent. */
  phiText: string;
};

export type BlurFixture = {
  /**
   * Draws the PHI text onto a canvas every animation frame, blurs it through
   * the real `compositeFrame`/`validateRegions` (capture-core), records the
   * canvas's own `MediaStream` with a real `MediaRecorder` producing an
   * actual video/webm blob, then decodes stills back out of that recording
   * via a real `<video>` element — the same "encode, then decode" path a
   * persisted capture goes through, minus the extension's chrome.* plumbing.
   */
  run(): Promise<BlurFixtureResult>;
};

declare global {
  interface Window {
    __htrBlurFixture: BlurFixture;
  }
}
