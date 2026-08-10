// Imported by submodule path, not the `@htr/capture-core` main barrel: the
// barrel re-exports storage/store (idb) and media (canvas/MediaRecorder)
// modules this SDK never touches, and every byte of those would otherwise
// ship to every host page that embeds this SDK.
import { PHI_PATTERNS } from '@htr/capture-core/src/redaction/patterns.js';
import { parseRuleset, type RedactionRuleset } from '@htr/capture-core/src/redaction/ruleset.js';

/**
 * The SDK has no policy channel (no extension, no build-time bundling step
 * of its own) to fetch a deployment-specific ruleset from, so — like
 * `clients/recording-link` — it ships the built-in synthetic-PHI pattern
 * library (spec §18) as its floor. A host application that also runs the
 * extension gets that ruleset's field-path/dom-selector rules too on capture;
 * this is what redacts metadata that never passes through the extension at all.
 */
export const SDK_RULESET_VERSION = '2026-08-04.sdk.1';

export const SDK_RULESET: RedactionRuleset = parseRuleset({
  version: SDK_RULESET_VERSION,
  rules: [...PHI_PATTERNS],
});
