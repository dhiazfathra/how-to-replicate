import { describe, expect, it } from 'vitest';
import type { CaptureEvent, NetworkPayload } from '../src/types/event.js';
import { createRedactor } from '../src/redaction/engine.js';
import { FIXTURES, FORBIDDEN_STRINGS, STANDARD_RULESET, truncateBody, type CorpusFixture } from './corpus/index.js';

/**
 * Fixture ids allowed to land as `fidelity: 'full'` — because their PHI is
 * correctly dropped by an upstream pipeline step (content-type policy)
 * before any redaction rule gets a chance to run, not because a rule failed
 * to fire. This is a gate the TEST FILE owns: exempting a fixture requires
 * editing this list, not setting a field on the fixture data itself.
 */
const NO_RULE_EXPECTED_IDS = new Set(['adversarial-disallowed-content-type']);

/**
 * Runs the real pre-redaction pipeline step for fixtures that declare
 * `truncateContentType`: content-type-based body dropping via `truncateBody`.
 * Every other fixture is handed to the redactor exactly as authored.
 */
function pipelineEvent(fixture: CorpusFixture): CaptureEvent {
  if (!fixture.truncateContentType) return fixture.event;
  const payload = fixture.event.payload as NetworkPayload;
  const result = truncateBody(payload.requestBody, fixture.truncateContentType);
  return {
    ...fixture.event,
    payload: { ...payload, requestBody: result.body, bodyDropped: result.bodyDropped, bodyTruncated: result.bodyTruncated },
  };
}

/**
 * Collects every string reachable in `value` — object keys AND string
 * values — by walking the structure directly rather than relying on
 * `JSON.stringify`, which a naive check could pass while still leaking PHI
 * hidden in a key or in a field `JSON.stringify` doesn't reach.
 */
function collectStrings(value: unknown, out: string[] = []): string[] {
  if (typeof value === 'string') {
    out.push(value);
    return out;
  }
  if (Array.isArray(value)) {
    for (const item of value) collectStrings(item, out);
    return out;
  }
  if (value !== null && typeof value === 'object') {
    for (const [key, val] of Object.entries(value)) {
      out.push(key);
      collectStrings(val, out);
    }
  }
  return out;
}

const redactor = createRedactor(STANDARD_RULESET);

describe('redaction corpus', () => {
  it('is non-trivial: the forbidden-string list is not empty', () => {
    expect(FORBIDDEN_STRINGS.length).toBeGreaterThan(0);
  });

  it.each(FIXTURES)('$id: leaks no forbidden string', (fixture: CorpusFixture) => {
    const outcome = redactor.redactEvent(pipelineEvent(fixture));
    const strings = collectStrings(outcome);
    for (const forbidden of FORBIDDEN_STRINGS) {
      for (const candidate of strings) {
        expect(candidate).not.toContain(forbidden);
      }
    }
  });

  it.each(FIXTURES.filter((f) => !NO_RULE_EXPECTED_IDS.has(f.id)))(
    '$id: does not pass through as fidelity "full"',
    (fixture: CorpusFixture) => {
      const outcome = redactor.redactEvent(pipelineEvent(fixture));
      expect(outcome.fidelity).not.toBe('full');
    },
  );

  it.each(FIXTURES.filter((f) => f.keyCountCheck))(
    '$id: key rewriting is non-destructive (no silent key merge)',
    (fixture: CorpusFixture) => {
      const check = fixture.keyCountCheck;
      if (!check) throw new Error('filtered fixture must have keyCountCheck');
      const outcome = redactor.redactEvent(pipelineEvent(fixture));
      if (outcome.fidelity === 'dropped') {
        throw new Error(`expected ${fixture.id} to survive redaction, got dropped: ${outcome.reason}`);
      }
      const redactedPayload = outcome.event.payload as { [K in typeof check.field]: string | null };
      const redactedBody = redactedPayload[check.field];
      if (redactedBody === null) throw new Error(`expected ${fixture.id} redacted body to be present`);
      const redactedObject = JSON.parse(redactedBody) as Record<string, unknown>;
      expect(Object.keys(redactedObject).length).toBe(Object.keys(check.original as object).length);
    },
  );
});
