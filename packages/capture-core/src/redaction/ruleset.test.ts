import { describe, expect, it } from 'vitest';
import { parseRuleset } from './ruleset.js';

describe('parseRuleset', () => {
  it('parses a well-formed ruleset with one of every rule class', () => {
    const parsed = parseRuleset({
      version: '1',
      rules: [
        { id: 'a', class: 'field-path', pointer: '/patient/name' },
        { id: 'b', class: 'header', name: 'Authorization' },
        { id: 'c', class: 'pattern', pattern: '\\d+', label: 'digits' },
        { id: 'd', class: 'pattern', pattern: '\\d+', flags: 'i', label: 'digits' },
        { id: 'e', class: 'dom-selector', selector: '.name' },
        { id: 'f', class: 'video-blur', selector: '#face' },
        { id: 'g', class: 'origin-allow', origins: ['https://example.com'] },
      ],
    });
    expect(parsed.version).toBe('1');
    expect(parsed.rules).toHaveLength(7);
  });

  it.each([
    ['not an object', 'nope'],
    ['null', null],
    ['array', []],
  ])('throws when the ruleset is %s', (_label, input) => {
    expect(() => parseRuleset(input)).toThrow();
  });

  it('throws when version is missing', () => {
    expect(() => parseRuleset({ rules: [] })).toThrow(/version/);
  });

  it('throws when version is empty', () => {
    expect(() => parseRuleset({ version: '', rules: [] })).toThrow(/version/);
  });

  it('throws when rules is not an array', () => {
    expect(() => parseRuleset({ version: '1', rules: {} })).toThrow(/rules/);
  });

  it('throws when a rule is not an object', () => {
    expect(() => parseRuleset({ version: '1', rules: ['nope'] })).toThrow();
  });

  it('throws when a rule id is missing', () => {
    expect(() => parseRuleset({ version: '1', rules: [{ class: 'header', name: 'X' }] })).toThrow(/id/);
  });

  it('throws on unknown rule class', () => {
    expect(() => parseRuleset({ version: '1', rules: [{ id: 'a', class: 'nope' }] })).toThrow(/unknown rule class/);
  });

  describe('field-path', () => {
    it('throws when pointer is missing', () => {
      expect(() =>
        parseRuleset({ version: '1', rules: [{ id: 'a', class: 'field-path' }] }),
      ).toThrow(/pointer/);
    });
  });

  describe('header', () => {
    it('throws when name is missing', () => {
      expect(() => parseRuleset({ version: '1', rules: [{ id: 'a', class: 'header' }] })).toThrow(/name/);
    });
  });

  describe('pattern', () => {
    it('throws when pattern is missing', () => {
      expect(() =>
        parseRuleset({ version: '1', rules: [{ id: 'a', class: 'pattern', label: 'x' }] }),
      ).toThrow(/pattern/);
    });

    it('throws when label is missing', () => {
      expect(() =>
        parseRuleset({ version: '1', rules: [{ id: 'a', class: 'pattern', pattern: '\\d+' }] }),
      ).toThrow(/label/);
    });

    it('throws when flags is not a string', () => {
      expect(() =>
        parseRuleset({
          version: '1',
          rules: [{ id: 'a', class: 'pattern', pattern: '\\d+', label: 'x', flags: 1 }],
        }),
      ).toThrow(/flags/);
    });

    it('throws on invalid regex source instead of dropping the rule', () => {
      expect(() =>
        parseRuleset({
          version: '1',
          rules: [{ id: 'a', class: 'pattern', pattern: '(', label: 'x' }],
        }),
      ).toThrow(/not a valid regular expression/);
    });
  });

  describe('dom-selector', () => {
    it('throws when selector is missing', () => {
      expect(() =>
        parseRuleset({ version: '1', rules: [{ id: 'a', class: 'dom-selector' }] }),
      ).toThrow(/selector/);
    });
  });

  describe('video-blur', () => {
    it('throws when selector is missing', () => {
      expect(() =>
        parseRuleset({ version: '1', rules: [{ id: 'a', class: 'video-blur' }] }),
      ).toThrow(/selector/);
    });
  });

  describe('origin-allow', () => {
    it('throws when origins is not an array', () => {
      expect(() =>
        parseRuleset({ version: '1', rules: [{ id: 'a', class: 'origin-allow', origins: 'x' }] }),
      ).toThrow(/origins/);
    });

    it('throws when origins contains a non-string', () => {
      expect(() =>
        parseRuleset({
          version: '1',
          rules: [{ id: 'a', class: 'origin-allow', origins: ['ok', 1] }],
        }),
      ).toThrow(/origins/);
    });

    it('accepts an empty origins array', () => {
      const parsed = parseRuleset({ version: '1', rules: [{ id: 'a', class: 'origin-allow', origins: [] }] });
      expect(parsed.rules[0]).toEqual({ id: 'a', class: 'origin-allow', origins: [] });
    });
  });
});
