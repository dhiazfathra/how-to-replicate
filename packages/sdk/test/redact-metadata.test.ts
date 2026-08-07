import { describe, expect, it } from 'vitest';
import { createRedactor } from '@htr/capture-core/src/redaction/engine.js';
import { ENGINE_INTERNAL_ERROR_RULE_ID, parseRuleset } from '@htr/capture-core/src/redaction/ruleset.js';
import type { CaptureEvent } from '@htr/capture-core/src/types/event.js';
// The golden synthetic-PHI corpus (spec §18) — the same fixtures
// `packages/capture-core/test/redaction-corpus.test.ts` gates every PR with.
// Test-only import (never shipped): proves SDK metadata redaction against
// the real forbidden-literal list, not a hand-rolled substitute.
import { FORBIDDEN_STRINGS, STANDARD_RULESET } from '../../capture-core/test/corpus/index.js';
import { redactMetadata } from '../src/redact-metadata.js';
import { SDK_RULESET } from '../src/ruleset.js';
import type { HtrMetadata } from '../src/types.js';

function collectStrings(value: unknown, out: string[] = []): string[] {
  if (typeof value === 'string') {
    out.push(value);
  } else if (Array.isArray(value)) {
    for (const item of value) collectStrings(item, out);
  } else if (value !== null && typeof value === 'object') {
    for (const [key, val] of Object.entries(value)) {
      out.push(key);
      collectStrings(val, out);
    }
  }
  return out;
}

// The corpus's two patient-name literals are redacted only by a `field-path`
// rule pinned to `/patient/name` (spec §18 note in patterns.ts: names have no
// generic regex, so they're redacted by schema position, not content). Free-
// text metadata has no such fixed schema — a `userId`/`note`/... field is
// never at that JSON Pointer — so those two literals are excluded from the
// freeform-placement leak scan below and covered separately, at the pointer
// a field-path rule can actually reach. One corpus literal is a raw
// backslash-`u`-escape adversarial string (testing JSON-encoding edge cases,
// not a plain content match) — embedding it as a literal JS string here
// changes its escaping once re-serialized, so it's excluded too; it is not a
// realistic `metadata()` value and the corpus's own suite already covers it
// at the layer it's meant for (a raw JSON request body).
const PATTERN_MATCHABLE_FORBIDDEN = FORBIDDEN_STRINGS.filter(
  (literal) => literal !== 'Siti Rahayu' && literal !== 'Siti Rahayu Kedua' && !literal.includes('\\'),
);

describe('redactMetadata', () => {
  it('leaks no pattern-matchable forbidden string from the golden corpus, nested at any depth', () => {
    const redactor = createRedactor(STANDARD_RULESET);
    const metadata: HtrMetadata = {
      userId: 'u-1',
      tenant: 'acme',
      nested: { note: `contact ${PATTERN_MATCHABLE_FORBIDDEN[0]}` },
      list: [...PATTERN_MATCHABLE_FORBIDDEN],
    };

    const snapshot = redactMetadata(redactor, metadata);

    const strings = collectStrings(snapshot);
    for (const forbidden of PATTERN_MATCHABLE_FORBIDDEN) {
      for (const candidate of strings) {
        expect(candidate).not.toContain(forbidden);
      }
    }
  });

  it('redacts a patient name placed at the field-path rule\'s exact schema position', () => {
    const redactor = createRedactor(STANDARD_RULESET);
    const snapshot = redactMetadata(redactor, { patient: { name: 'Siti Rahayu' } });
    expect(collectStrings(snapshot)).not.toContain('Siti Rahayu');
    expect(snapshot.redaction.rulesApplied).toContain('fp:patient-name');
  });

  it('marks fidelity redacted and lists the rule when a field is scrubbed', () => {
    const redactor = createRedactor(SDK_RULESET);
    const snapshot = redactMetadata(redactor, { email: 'siti.rahayu@example.com' });

    expect(snapshot.source).toBe('sdk');
    expect(snapshot.redaction.fidelity).toBe('redacted');
    expect(snapshot.redaction.rulesApplied).toContain('builtin:email');
    expect(snapshot.metadata.email).toBe('[REDACTED:email]');
  });

  it('leaves clean metadata at full fidelity, untouched', () => {
    const redactor = createRedactor(SDK_RULESET);
    const input: HtrMetadata = { userId: 'u-1', tenant: 'acme', buildSha: 'abc123', featureFlags: { x: true } };

    const snapshot = redactMetadata(redactor, input);

    expect(snapshot.redaction.fidelity).toBe('full');
    expect(snapshot.redaction.rulesApplied).toEqual([]);
    expect(snapshot.metadata).toEqual(input);
  });

  it('degrades to an empty, dropped snapshot rather than leak on an engine-internal failure', () => {
    // A ruleset with a pattern that throws is impossible to construct
    // through parseRuleset (it validates the regex compiles), so this
    // exercises the drop path via a redactor whose redactEvent always
    // drops — the same contract the real engine guarantees on internal
    // failure (invariant 1: never let content through unredacted on error).
    const alwaysDrops = {
      redactEvent: () => ({ fidelity: 'dropped' as const, ruleId: ENGINE_INTERNAL_ERROR_RULE_ID, reason: 'boom' }),
      isOriginAllowed: () => false,
      blurSelectors: () => [],
    };

    const snapshot = redactMetadata(alwaysDrops, { userId: 'u-1' });

    expect(snapshot).toEqual({
      source: 'sdk',
      metadata: {},
      redaction: { rulesApplied: [ENGINE_INTERNAL_ERROR_RULE_ID], fidelity: 'dropped' },
    });
  });

  it('treats a null requestBody outcome (defensive) as empty metadata', () => {
    const passThroughButNulled = {
      redactEvent: (event: CaptureEvent) => ({
        fidelity: 'full' as const,
        event: {
          ...event,
          payload: {
            ...(event.payload as Record<string, unknown>),
            requestBody: null,
          } as CaptureEvent['payload'],
          redaction: { rulesApplied: [], fidelity: 'full' as const },
        },
      }),
      isOriginAllowed: () => false,
      blurSelectors: () => [],
    };

    const snapshot = redactMetadata(passThroughButNulled, { userId: 'u-1' });

    expect(snapshot.metadata).toEqual({});
  });

  it('accepts an empty metadata object', () => {
    const redactor = createRedactor(parseRuleset({ version: '1', rules: [] }));
    const snapshot = redactMetadata(redactor, {});
    expect(snapshot).toEqual({ source: 'sdk', metadata: {}, redaction: { rulesApplied: [], fidelity: 'full' } });
  });
});
