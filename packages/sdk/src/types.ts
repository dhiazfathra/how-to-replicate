// Type-only import: erased at build time (verbatimModuleSyntax), so this
// costs the bundle nothing despite `@htr/capture-core` being a heavy
// package overall — see index.ts for why only this and the redaction
// submodule (never the package's main barrel) are imported.
import type { JsonValue } from '@htr/capture-core/src/types/asset.js';

/** Arbitrary JSON-serializable context a host app attaches via `htr.metadata()`. */
export type HtrMetadata = Record<string, JsonValue>;

/**
 * What `htr.metadata()` / `htr.snapshot()` return: the metadata as it will be
 * written to `Capture.metadata`, post-redaction, plus the same
 * `{ rulesApplied, fidelity }` shape `CaptureEvent.redaction` already uses
 * elsewhere in this codebase — so a dropped field is traceable the same way
 * a dropped event is, not via a new ad-hoc mechanism.
 */
export type MetadataSnapshot = {
  source: 'sdk';
  metadata: HtrMetadata;
  redaction: { rulesApplied: string[]; fidelity: 'full' | 'redacted' | 'dropped' };
};
