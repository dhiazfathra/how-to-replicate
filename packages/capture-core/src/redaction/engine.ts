import type {
  CaptureEvent,
  InteractionPayload,
  NetworkPayload,
} from '../types/event.js';
import type { RedactionRule, RedactionRuleset } from './ruleset.js';

export type RedactionOutcome =
  | { fidelity: 'full' | 'redacted'; event: CaptureEvent }
  | { fidelity: 'dropped'; ruleId: string; reason: string };

export type Redactor = {
  redactEvent(event: CaptureEvent): RedactionOutcome;
  isOriginAllowed(origin: string): boolean;
  blurSelectors(): string[];
};

type ApplyResult = { changed: boolean; payload: CaptureEvent['payload'] };

/** Reserved ruleId for a drop caused by the engine itself, not a specific rule. */
const ENGINE_INTERNAL_ERROR = 'engine:internal-error';

/**
 * Build a Redactor from a parsed ruleset. `redactEvent` is pure and total: for
 * the same event and ruleset it always returns the same outcome, and it never
 * throws — any failure (a single rule's, or the engine's own) is converted to
 * a drop. An error must never let an event through unredacted.
 */
export function createRedactor(ruleset: RedactionRuleset): Redactor {
  const allowedOrigins = new Set(
    ruleset.rules.filter(isOriginAllowRule).flatMap((rule) => rule.origins),
  );
  const blurSelectors = ruleset.rules.filter(isVideoBlurRule).map((rule) => rule.selector);

  return {
    redactEvent(event: CaptureEvent): RedactionOutcome {
      try {
        let working: CaptureEvent['payload'];
        try {
          working = structuredClone(event.payload);
        } catch {
          return {
            fidelity: 'dropped',
            ruleId: ENGINE_INTERNAL_ERROR,
            reason: 'failed to clone event payload',
          };
        }

        const applied: string[] = [];
        for (const rule of ruleset.rules) {
          let result: ApplyResult;
          try {
            result = applyRule(rule, event.kind, working);
          } catch {
            return {
              fidelity: 'dropped',
              ruleId: rule.id,
              reason: `rule evaluation failed (class: ${rule.class})`,
            };
          }
          working = result.payload;
          if (result.changed) applied.push(rule.id);
        }

        const rulesApplied = [...new Set(applied)];
        const fidelity: 'full' | 'redacted' = rulesApplied.length > 0 ? 'redacted' : 'full';
        return {
          fidelity,
          event: { ...event, payload: working, redaction: { rulesApplied, fidelity } },
        };
      } catch {
        // Backstop for any failure not attributable to a specific rule.
        return {
          fidelity: 'dropped',
          ruleId: ENGINE_INTERNAL_ERROR,
          reason: 'unexpected failure during redaction',
        };
      }
    },

    isOriginAllowed(origin: string): boolean {
      return allowedOrigins.has(origin);
    },

    blurSelectors(): string[] {
      return [...blurSelectors];
    },
  };
}

function isOriginAllowRule(rule: RedactionRule): rule is Extract<RedactionRule, { class: 'origin-allow' }> {
  return rule.class === 'origin-allow';
}

function isVideoBlurRule(rule: RedactionRule): rule is Extract<RedactionRule, { class: 'video-blur' }> {
  return rule.class === 'video-blur';
}

function applyRule(
  rule: RedactionRule,
  kind: CaptureEvent['kind'],
  payload: CaptureEvent['payload'],
): ApplyResult {
  switch (rule.class) {
    case 'field-path':
      return applyFieldPath(rule.pointer, kind, payload);
    case 'header':
      return applyHeader(rule.name, kind, payload);
    case 'pattern':
      return applyPattern(rule.pattern, rule.flags, rule.label, kind, payload);
    case 'dom-selector':
      return applyDomSelector(rule.selector, kind, payload);
    case 'video-blur':
    case 'origin-allow':
      // Not per-event rules: video-blur is surfaced via blurSelectors(), and
      // origin-allow via isOriginAllowed(). Neither touches an event payload.
      return { changed: false, payload };
  }
}

// --- field-path ------------------------------------------------------------

function applyFieldPath(
  pointer: string,
  kind: CaptureEvent['kind'],
  payload: CaptureEvent['payload'],
): ApplyResult {
  if (kind !== 'network') return { changed: false, payload };
  const np = payload as NetworkPayload;
  const segments = parsePointer(pointer);
  let changed = false;
  const next: NetworkPayload = { ...np };

  for (const key of ['requestBody', 'responseBody'] as const) {
    const raw = next[key];
    if (raw === null) continue;
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      // Not JSON — field-path does not apply; pattern rules still see it.
      continue;
    }
    const result = redactAtPointer(parsed, segments);
    if (result.changed) {
      changed = true;
      next[key] = JSON.stringify(result.value);
    }
  }

  return { changed, payload: next };
}

function parsePointer(pointer: string): string[] {
  if (pointer === '' || pointer === '/') return [];
  const raw = pointer.startsWith('/') ? pointer.slice(1) : pointer;
  return raw.split('/').map((segment) => segment.replace(/~1/g, '/').replace(/~0/g, '~'));
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function redactAtPointer(value: unknown, segments: string[]): { value: unknown; changed: boolean } {
  if (segments.length === 0) {
    return { value: '[REDACTED]', changed: true };
  }
  const [segment, ...rest] = segments as [string, ...string[]];

  if (segment === '*') {
    if (Array.isArray(value)) {
      let changed = false;
      const next = value.map((item: unknown) => {
        const result = redactAtPointer(item, rest);
        if (result.changed) changed = true;
        return result.value;
      });
      return { value: next, changed };
    }
    if (isPlainObject(value)) {
      let changed = false;
      const next: Record<string, unknown> = { ...value };
      for (const key of Object.keys(next)) {
        const result = redactAtPointer(next[key], rest);
        if (result.changed) changed = true;
        next[key] = result.value;
      }
      return { value: next, changed };
    }
    throw new Error('field-path: wildcard segment on a non-container value');
  }

  if (Array.isArray(value)) {
    const index = Number(segment);
    if (!Number.isInteger(index) || index < 0 || index >= value.length) {
      return { value, changed: false };
    }
    const next = value.slice();
    const result = redactAtPointer(next[index], rest);
    next[index] = result.value;
    return { value: next, changed: result.changed };
  }

  if (isPlainObject(value)) {
    if (!(segment in value)) {
      return { value, changed: false };
    }
    const next: Record<string, unknown> = { ...value };
    const result = redactAtPointer(next[segment], rest);
    next[segment] = result.value;
    return { value: next, changed: result.changed };
  }

  throw new Error('field-path: pointer segment on a non-container value');
}

// --- header ------------------------------------------------------------

function applyHeader(
  name: string,
  kind: CaptureEvent['kind'],
  payload: CaptureEvent['payload'],
): ApplyResult {
  if (kind !== 'network') return { changed: false, payload };
  const np = payload as NetworkPayload;
  const lowerName = name.toLowerCase();

  const redactHeaders = (headers: Record<string, string>): { headers: Record<string, string>; changed: boolean } => {
    let changed = false;
    const next: Record<string, string> = {};
    for (const [key, value] of Object.entries(headers)) {
      if (key.toLowerCase() === lowerName) {
        next[key] = '[REDACTED]';
        changed = true;
      } else {
        next[key] = value;
      }
    }
    return { headers: next, changed };
  };

  const request = redactHeaders(np.requestHeaders);
  const response = redactHeaders(np.responseHeaders);
  if (!request.changed && !response.changed) return { changed: false, payload };

  return {
    changed: true,
    payload: { ...np, requestHeaders: request.headers, responseHeaders: response.headers },
  };
}

// --- pattern ------------------------------------------------------------

function compilePatternRegex(pattern: string, flags: string | undefined): RegExp {
  const withGlobal = (flags ?? '').includes('g') ? (flags as string) : `${flags ?? ''}g`;
  return new RegExp(pattern, withGlobal);
}

function applyPatternToString(value: string, regex: RegExp, label: string): { value: string; changed: boolean } {
  let changed = false;
  const result = value.replace(regex, () => {
    changed = true;
    return `[REDACTED:${label}]`;
  });
  return { value: result, changed };
}

/**
 * Recursively apply a pattern to every string reachable in a JSON-like value,
 * including object keys. Key rewrites are collision-safe: if two keys in the
 * same object redact to the same string, later ones (by iteration order) get
 * an ordinal suffix (`#2`, `#3`, ...) instead of overwriting a sibling.
 */
function deepPatternWalk(value: unknown, regex: RegExp, label: string): { value: unknown; changed: boolean } {
  if (typeof value === 'string') {
    return applyPatternToString(value, regex, label);
  }

  if (Array.isArray(value)) {
    let changed = false;
    const next = value.map((item: unknown) => {
      const result = deepPatternWalk(item, regex, label);
      if (result.changed) changed = true;
      return result.value;
    });
    return { value: next, changed };
  }

  if (isPlainObject(value)) {
    let changed = false;
    const next: Record<string, unknown> = {};
    const entries = Object.entries(value);
    entries.forEach(([key, val], index) => {
      const keyResult = applyPatternToString(key, regex, label);
      if (keyResult.changed) changed = true;
      const valueResult = deepPatternWalk(val, regex, label);
      if (valueResult.changed) changed = true;

      let finalKey = keyResult.value;
      if (Object.prototype.hasOwnProperty.call(next, finalKey)) {
        finalKey = `${finalKey}#${index + 1}`;
      }
      next[finalKey] = valueResult.value;
    });
    return { value: next, changed };
  }

  return { value, changed: false };
}

function applyPattern(
  pattern: string,
  flags: string | undefined,
  label: string,
  kind: CaptureEvent['kind'],
  payload: CaptureEvent['payload'],
): ApplyResult {
  const regex = compilePatternRegex(pattern, flags);

  if (kind === 'network') {
    const np = payload as NetworkPayload;
    let changed = false;
    const next: NetworkPayload = { ...np };

    for (const key of ['requestBody', 'responseBody'] as const) {
      const raw = next[key];
      if (raw === null) continue;
      let parsed: unknown;
      let isJson = true;
      try {
        parsed = JSON.parse(raw);
      } catch {
        isJson = false;
      }
      if (isJson) {
        const walked = deepPatternWalk(parsed, regex, label);
        if (walked.changed) {
          changed = true;
          next[key] = JSON.stringify(walked.value);
        }
      } else {
        const walked = applyPatternToString(raw, regex, label);
        if (walked.changed) {
          changed = true;
          next[key] = walked.value;
        }
      }
    }

    // Bodies are already handled above (JSON-aware); walk everything else
    // (url, method, headers, status, ...) generically for the same pattern,
    // leaving the already-processed bodies untouched.
    const rest: Record<string, unknown> = { ...next };
    delete rest.requestBody;
    delete rest.responseBody;
    const walkedRest = deepPatternWalk(rest, regex, label);
    if (walkedRest.changed) changed = true;

    return {
      changed,
      payload: {
        ...(walkedRest.value as Record<string, unknown>),
        requestBody: next.requestBody,
        responseBody: next.responseBody,
      } as NetworkPayload,
    };
  }

  const walked = deepPatternWalk(payload, regex, label);
  return { changed: walked.changed, payload: walked.value as CaptureEvent['payload'] };
}

// --- dom-selector ------------------------------------------------------------

function applyDomSelector(
  selector: string,
  kind: CaptureEvent['kind'],
  payload: CaptureEvent['payload'],
): ApplyResult {
  if (kind !== 'interaction') return { changed: false, payload };
  const ip = payload as InteractionPayload;
  if (ip.targetSelector !== selector) return { changed: false, payload };

  return {
    changed: true,
    payload: {
      ...ip,
      targetName: '[REDACTED]',
      value: ip.value === null ? null : '[REDACTED]',
    },
  };
}
