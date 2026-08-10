// Exports the Phase 0 golden redaction corpus (packages/capture-core/test/corpus)
// as JSON so services/redaction-audit's Go conformance suite can be driven by
// the SAME fixtures as this package's own redaction-corpus.test.ts, instead of
// a hand-copied duplicate that could silently drift from the source of truth.
//
// Run: node scripts/export-audit-corpus.ts <output-path>
// Regenerate whenever the corpus or STANDARD_RULESET changes; CI's
// conformance test fails loudly if the checked-in output is stale (see
// services/redaction-audit/internal/redact/conformance_test.go).
import { writeFileSync } from 'node:fs';
import type { NetworkPayload } from '../src/types/event.js';
import type { CaptureEvent } from '../src/types/event.js';
import { createRedactor } from '../src/redaction/engine.js';
import { FIXTURES, STANDARD_RULESET, truncateBody, type CorpusFixture } from '../test/corpus/index.js';

// Mirrors redaction-corpus.test.ts's pipelineEvent: runs the real
// content-type truncation step for fixtures that declare it, so the
// exported event is what would actually reach the redactor (and, in
// production, what would actually cross the network boundary) rather than
// the fixture's raw pre-pipeline authoring shape.
function pipelineEvent(fixture: CorpusFixture): CaptureEvent {
  if (!fixture.truncateContentType) return fixture.event;
  const payload = fixture.event.payload as NetworkPayload;
  const result = truncateBody(payload.requestBody, fixture.truncateContentType);
  return {
    ...fixture.event,
    payload: { ...payload, requestBody: result.body, bodyDropped: result.bodyDropped, bodyTruncated: result.bodyTruncated },
  };
}

const outPath = process.argv[2];
if (!outPath) {
  console.error('usage: export-audit-corpus.ts <output-path>');
  process.exit(1);
}

const redactor = createRedactor(STANDARD_RULESET);

const fixtures = FIXTURES.map((fixture) => {
  const event = pipelineEvent(fixture);
  const outcome = redactor.redactEvent(event);
  const tsRulesApplied = outcome.fidelity === 'dropped' ? [] : outcome.event.redaction.rulesApplied;
  return {
    id: fixture.id,
    kind: event.kind,
    payload: event.payload,
    forbidden: fixture.forbidden,
    tsFidelity: outcome.fidelity,
    tsRulesApplied,
  };
});

const out = {
  rulesetVersion: STANDARD_RULESET.version,
  ruleset: STANDARD_RULESET,
  fixtures,
};

writeFileSync(outPath, JSON.stringify(out, null, 2) + '\n');
console.log(`wrote ${fixtures.length} fixtures to ${outPath}`);
