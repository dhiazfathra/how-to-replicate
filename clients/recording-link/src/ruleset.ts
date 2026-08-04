import { parseRuleset, type RedactionRuleset } from '@htr/capture-core';

/**
 * recording-link has no extension/enterprise-policy channel to fetch a
 * ruleset from at capture time (spec §13's "no-login" constraint), so the
 * ruleset is bundled into the page at build time instead. Its version is
 * stamped onto every capture's metadata (see `capture-flow.ts`) so a gap in
 * redaction coverage can always be traced back to exactly which ruleset
 * produced the capture — the same accountability the extension gets from
 * its enterprise-policy ruleset version, by a different delivery mechanism.
 */
export const BUNDLED_RULESET_VERSION = '2026-08-04.recording-link.1';

export const BUNDLED_RULESET: RedactionRuleset = parseRuleset({
  version: BUNDLED_RULESET_VERSION,
  rules: [
    {
      id: 'pattern:email',
      class: 'pattern',
      pattern: '[\\w.+-]+@[\\w-]+\\.[\\w.-]+',
      label: 'email',
    },
    {
      id: 'pattern:phone',
      class: 'pattern',
      pattern: '\\+?\\d[\\d\\-\\s()]{7,}\\d',
      label: 'phone',
    },
  ],
});
