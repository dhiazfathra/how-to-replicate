export type RuleClass =
  | 'field-path'
  | 'header'
  | 'pattern'
  | 'dom-selector'
  | 'video-blur'
  | 'origin-allow';

export type FieldPathRule = { id: string; class: 'field-path'; pointer: string };
export type HeaderRule = { id: string; class: 'header'; name: string };
export type PatternRule = {
  id: string;
  class: 'pattern';
  pattern: string;
  flags?: string;
  label: string;
};
export type DomSelectorRule = { id: string; class: 'dom-selector'; selector: string };
export type VideoBlurRule = { id: string; class: 'video-blur'; selector: string };
export type OriginAllowRule = { id: string; class: 'origin-allow'; origins: string[] };

export type RedactionRule =
  | FieldPathRule
  | HeaderRule
  | PatternRule
  | DomSelectorRule
  | VideoBlurRule
  | OriginAllowRule;

export type RedactionRuleset = {
  version: string;
  rules: RedactionRule[];
};

/** Reserved for the engine's own internal-failure drops — never a valid rule id. */
export const ENGINE_INTERNAL_ERROR_RULE_ID = 'engine:internal-error';

/**
 * Parse and validate an unknown value into a RedactionRuleset. Throws on any
 * malformed input rather than silently dropping an unparseable rule — a
 * dropped rule at parse time is a hole in the compliance surface, not a
 * recoverable edge case.
 */
export function parseRuleset(input: unknown): RedactionRuleset {
  if (typeof input !== 'object' || input === null) {
    throw new Error('ruleset: expected an object');
  }
  const obj = input as Record<string, unknown>;
  if (typeof obj.version !== 'string' || obj.version.length === 0) {
    throw new Error('ruleset: "version" must be a non-empty string');
  }
  if (!Array.isArray(obj.rules)) {
    throw new Error('ruleset: "rules" must be an array');
  }
  const rules = obj.rules.map((rule, index) => parseRule(rule, index));

  const seenIds = new Set<string>();
  for (const rule of rules) {
    if (rule.id === ENGINE_INTERNAL_ERROR_RULE_ID) {
      throw new Error(`ruleset: rule id "${rule.id}" is reserved for engine-internal failures`);
    }
    if (seenIds.has(rule.id)) {
      throw new Error(`ruleset: duplicate rule id "${rule.id}"`);
    }
    seenIds.add(rule.id);
  }

  return { version: obj.version, rules };
}

function requireNonEmptyString(value: unknown, index: number, id: string, field: string): string {
  if (typeof value !== 'string' || value.length === 0) {
    throw new Error(`rule[${index}] (${id}): "${field}" must be a non-empty string`);
  }
  return value;
}

function parseRule(input: unknown, index: number): RedactionRule {
  if (typeof input !== 'object' || input === null) {
    throw new Error(`rule[${index}]: expected an object`);
  }
  const raw = input as Record<string, unknown>;
  const id = requireNonEmptyString(raw.id, index, '<unknown>', 'id');

  switch (raw.class) {
    case 'field-path': {
      // Empty string is the RFC 6901 root pointer — a legitimate value, not
      // a malformed rule — so this field only requires the type to be right.
      if (typeof raw.pointer !== 'string') {
        throw new Error(`rule[${index}] (${id}): "pointer" must be a string`);
      }
      return { id, class: 'field-path', pointer: raw.pointer };
    }

    case 'header':
      return { id, class: 'header', name: requireNonEmptyString(raw.name, index, id, 'name') };

    case 'pattern': {
      const pattern = requireNonEmptyString(raw.pattern, index, id, 'pattern');
      const label = requireNonEmptyString(raw.label, index, id, 'label');
      if (raw.flags !== undefined && typeof raw.flags !== 'string') {
        throw new Error(`rule[${index}] (${id}): "flags" must be a string when present`);
      }
      const flags = raw.flags;
      try {
        // Validating the regex compiles; invalid source is a parse failure.
        new RegExp(pattern, flags);
      } catch {
        throw new Error(`rule[${index}] (${id}): "pattern" is not a valid regular expression`);
      }
      return flags === undefined
        ? { id, class: 'pattern', pattern, label }
        : { id, class: 'pattern', pattern, flags, label };
    }

    case 'dom-selector':
      return { id, class: 'dom-selector', selector: requireNonEmptyString(raw.selector, index, id, 'selector') };

    case 'video-blur':
      return { id, class: 'video-blur', selector: requireNonEmptyString(raw.selector, index, id, 'selector') };

    case 'origin-allow': {
      if (!Array.isArray(raw.origins) || raw.origins.some((origin) => typeof origin !== 'string')) {
        throw new Error(`rule[${index}] (${id}): "origins" must be an array of strings`);
      }
      return { id, class: 'origin-allow', origins: raw.origins as string[] };
    }

    default:
      throw new Error(`rule[${index}] (${id}): unknown rule class ${JSON.stringify(raw.class)}`);
  }
}
