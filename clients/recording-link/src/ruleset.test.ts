import { describe, expect, it } from 'vitest';
import { BUNDLED_RULESET, BUNDLED_RULESET_VERSION } from './ruleset.js';

describe('BUNDLED_RULESET', () => {
  it('parses into a valid ruleset stamped with the bundled version', () => {
    expect(BUNDLED_RULESET.version).toBe(BUNDLED_RULESET_VERSION);
    expect(BUNDLED_RULESET.rules.length).toBeGreaterThan(0);
  });
});
