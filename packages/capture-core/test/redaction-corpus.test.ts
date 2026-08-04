import { describe, expect, it } from 'vitest';
import { createRedactor } from '../src/redaction/engine.js';
import { FIXTURES, FORBIDDEN_STRINGS, STANDARD_RULESET, type CorpusFixture } from './corpus/index.js';

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
    const outcome = redactor.redactEvent(fixture.event);
    const strings = collectStrings(outcome);
    for (const forbidden of FORBIDDEN_STRINGS) {
      for (const candidate of strings) {
        expect(candidate.includes(forbidden)).toBe(false);
      }
    }
  });

  it.each(FIXTURES.filter((f) => f.expectRedacted !== false))(
    '$id: does not pass through as fidelity "full"',
    (fixture: CorpusFixture) => {
      const outcome = redactor.redactEvent(fixture.event);
      expect(outcome.fidelity).not.toBe('full');
    },
  );

  it.each(FIXTURES.filter((f) => f.keyCountCheck))(
    '$id: key rewriting is non-destructive (no silent key merge)',
    (fixture: CorpusFixture) => {
      const check = fixture.keyCountCheck;
      if (!check) throw new Error('filtered fixture must have keyCountCheck');
      const outcome = redactor.redactEvent(fixture.event);
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
