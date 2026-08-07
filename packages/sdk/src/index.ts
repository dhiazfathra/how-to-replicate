// Runtime import from the redaction submodule only — never the
// `@htr/capture-core` main barrel. See ruleset.ts for why: the barrel pulls
// in idb-backed storage and media/blur code this SDK never runs, which would
// otherwise ship into every host page.
import { createRedactor } from '@htr/capture-core/src/redaction/engine.js';
import { SDK_RULESET } from './ruleset.js';
import { redactMetadata } from './redact-metadata.js';
import type { HtrMetadata, MetadataSnapshot } from './types.js';

export type { HtrMetadata, MetadataSnapshot } from './types.js';

export type HtrSdk = {
  /**
   * Merge `input` into the accumulated metadata context and return the
   * redacted snapshot to attach to `Capture.metadata` (source: 'sdk'). Safe
   * to call more than once per capture (e.g. `userId` at init,
   * `featureFlags` later) — later calls merge over earlier ones, last write
   * wins per key.
   */
  metadata(input: HtrMetadata): MetadataSnapshot;
  /** The current redacted snapshot, without adding anything new. */
  snapshot(): MetadataSnapshot;
};

/**
 * Each instance owns its own accumulated metadata and its own redactor — no
 * shared mutable module state, so tests (and multiple embeds on one page)
 * don't leak into each other.
 */
export function createHtrSdk(): HtrSdk {
  const redactor = createRedactor(SDK_RULESET);
  let stored: HtrMetadata = {};

  return {
    metadata(input: HtrMetadata): MetadataSnapshot {
      stored = { ...stored, ...input };
      return redactMetadata(redactor, stored);
    },
    snapshot(): MetadataSnapshot {
      return redactMetadata(redactor, stored);
    },
  };
}

/** Default embed: `import { htr } from '@htr/sdk'; htr.metadata({ userId })`. */
export const htr: HtrSdk = createHtrSdk();
