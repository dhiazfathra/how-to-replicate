import type { PatternRule } from './ruleset.js';

/**
 * Built-in synthetic-PHI pattern library (spec §18). Ready-made `pattern`
 * rules — a ruleset includes them by spreading `PHI_PATTERNS`.
 */
export const PHI_PATTERNS: PatternRule[] = [
  // phone-id is checked first: its Indonesian mobile prefix is more specific
  // than the plain digit-count patterns below, which would otherwise win a
  // number like "+6281234567890" (13 digits after the leading "+") away from
  // nik/bpjs before phone-id got a chance to match the whole thing.
  {
    id: 'builtin:phone-id',
    class: 'pattern',
    // A leading "+" isn't a word character, so `\b` never holds right before
    // it — use a not-preceded-by-digit lookbehind instead so the "+62" form
    // actually matches (and matches whole, "+" included).
    pattern: '(?<!\\d)(?:\\+?62|0)8\\d{7,11}\\b',
    label: 'phone-id',
  },
  { id: 'builtin:nik', class: 'pattern', pattern: '\\b\\d{16}\\b', label: 'nik' },
  { id: 'builtin:bpjs', class: 'pattern', pattern: '\\b\\d{13}\\b', label: 'bpjs' },
  { id: 'builtin:mrn', class: 'pattern', pattern: '\\bMRN[-\\s]?\\d{4,10}\\b', flags: 'i', label: 'mrn' },
  {
    id: 'builtin:email',
    class: 'pattern',
    pattern: '\\b[\\w.+-]+@[\\w-]+\\.[A-Za-z]{2,}\\b',
    label: 'email',
  },
  {
    id: 'builtin:dob',
    class: 'pattern',
    pattern: '\\b(?:\\d{4}-\\d{2}-\\d{2}|\\d{2}/\\d{2}/\\d{4})\\b',
    label: 'dob',
  },
];
