import type { Redactor } from '@htr/capture-core/src/redaction/engine.js';
import type { CaptureEvent, NetworkPayload } from '@htr/capture-core/src/types/event.js';
import type { HtrMetadata, MetadataSnapshot } from './types.js';

/**
 * Metadata has no natural `CaptureEvent.kind` of its own — the closest fit is
 * `network`'s `requestBody`, the one payload field the engine already treats
 * as freeform JSON content (`deepPatternWalk`: every string reachable in the
 * parsed body, keys AND values, same as a captured request/response). This
 * routes SDK metadata through the exact same redactor Phase 0 built, with no
 * new redaction logic — the requirement in spec §5.1 — rather than
 * reimplementing a JSON-walking scan here.
 */
function toSyntheticEvent(metadata: HtrMetadata): CaptureEvent {
  const payload: NetworkPayload = {
    method: 'SDK',
    url: '',
    status: null,
    requestHeaders: {},
    responseHeaders: {},
    requestBody: JSON.stringify(metadata),
    responseBody: null,
    bodyTruncated: false,
    bodyDropped: false,
    durationMs: null,
    sizeBytes: null,
  };
  return {
    id: 'sdk-metadata',
    captureId: '',
    t: 0,
    kind: 'network',
    payload,
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

/**
 * Redact a metadata object through `redactor` and return it in the shape
 * `Capture.metadata` expects, plus a `redaction` record consistent with
 * `CaptureEvent.redaction`. Never throws: `redactor.redactEvent` is total,
 * and an engine-internal failure surfaces as `fidelity: 'dropped'` here too
 * (invariant 1 — no unredacted content escapes, even on error).
 */
export function redactMetadata(redactor: Redactor, metadata: HtrMetadata): MetadataSnapshot {
  const outcome = redactor.redactEvent(toSyntheticEvent(metadata));

  if (outcome.fidelity === 'dropped') {
    return { source: 'sdk', metadata: {}, redaction: { rulesApplied: [outcome.ruleId], fidelity: 'dropped' } };
  }

  const payload = outcome.event.payload as NetworkPayload;
  const redacted = payload.requestBody === null ? {} : (JSON.parse(payload.requestBody) as HtrMetadata);
  return { source: 'sdk', metadata: redacted, redaction: outcome.event.redaction };
}
