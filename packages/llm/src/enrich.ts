import type {
  AnnotationPayload,
  CaptureEvent,
  ConsolePayload,
  InteractionPayload,
  LifecyclePayload,
  NavigationPayload,
  NetworkPayload,
  ReplicationDoc,
  Step,
} from '@htr/capture-core';
import { providerBrand, type LlmProvider } from './provider.js';

export type EnrichDocInput = {
  doc: ReplicationDoc;
  events: CaptureEvent[];
  provider: LlmProvider;
  timeoutMs?: number;
};

type StepCandidate = {
  text: string;
  eventIds: string[];
};

/**
 * Defense-in-depth for the ADR-007 local-only policy: `selectProvider` is
 * meant to be the only place a non-local provider can reach this function,
 * but enforcing it again here means a future caller that skips
 * `selectProvider` still can't leak capture events to a remote endpoint.
 */
function isLocalProvider(provider: LlmProvider): boolean {
  return (
    provider[providerBrand] === true &&
    (provider.target === 'localhost' || provider.target === 'native-messaging')
  );
}

function isStepCandidate(value: unknown): value is StepCandidate {
  if (typeof value !== 'object' || value === null) return false;
  const text = (value as { text?: unknown }).text;
  const eventIds = (value as { eventIds?: unknown }).eventIds;
  return (
    typeof text === 'string' &&
    Array.isArray(eventIds) &&
    eventIds.every((id) => typeof id === 'string')
  );
}

/** Case-insensitive substring match; an empty needle never matches — a step
 * describing nothing about the event is not evidence, it's a coincidence. */
function includesText(haystack: string, needle: string): boolean {
  const trimmed = needle.trim().toLowerCase();
  return trimmed !== '' && haystack.includes(trimmed);
}

const INTERACTION_VERBS: Record<InteractionPayload['type'], string> = {
  click: 'clicked',
  input: 'typed',
  keydown: 'pressed',
  scroll: 'scrolled',
  submit: 'submitted',
  mousemove: 'moved',
};
const CONSOLE_LEVELS: ConsolePayload['level'][] = ['log', 'info', 'warn', 'error', 'debug'];
const NETWORK_METHODS = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH'];
const NAVIGATION_TRIGGERS: NavigationPayload['trigger'][] = [
  'load',
  'pushstate',
  'popstate',
  'replacestate',
  'hashchange',
];

/**
 * Whole-word, case-insensitive match. Plain `.includes()` would let "get"
 * match inside "target" or "log" match inside "login" — a coincidental
 * substring hit, not a real claim about the word. `\b` word boundaries rule
 * that out; every option word here is plain alphanumeric, so no escaping is
 * needed.
 */
function containsWord(text: string, word: string): boolean {
  return new RegExp(`\\b${word}\\b`, 'i').test(text);
}

/**
 * A step can quote a real, cited event's payload verbatim while still
 * asserting something false about it — "Clicked Save" anchored to an
 * `input` event whose target happens to be named "Save", for instance. For
 * kinds with an enum-like discriminator, this is a cheap, deterministic
 * fabrication check: if the step names a sibling value of that enum (the
 * wrong interaction verb, console level, or HTTP method/trigger) and never
 * names the event's actual value, it's describing the wrong thing and must
 * not survive on the strength of an unrelated substring match.
 */
function namesWrongOption(text: string, options: readonly string[], correct: string): boolean {
  const namesCorrect = containsWord(text, correct);
  const namesWrongSibling = options.some(
    (option) => option.toLowerCase() !== correct.toLowerCase() && containsWord(text, option),
  );
  return namesWrongSibling && !namesCorrect;
}

/**
 * A 3-digit number in the text that isn't the event's actual HTTP status.
 * `url` is stripped first so a path segment like `/patients/123` isn't
 * mistaken for a claimed status code. Only called once `includesText` has
 * already confirmed `url` is a non-empty substring of `text`.
 */
function namesWrongStatus(text: string, url: string, status: number | null): boolean {
  if (status === null) return false;
  const withoutUrl = text.split(url.toLowerCase()).join(' ');
  const mentioned = withoutUrl.match(/\b\d{3}\b/g) ?? [];
  return mentioned.some((code) => Number(code) !== status);
}

/**
 * Validates that `text` actually describes `event`'s salient payload
 * fields — citing a real event ID is necessary but not sufficient, the step
 * must also be truthful about what that event contains (ADR-007).
 */
function describesEvent(text: string, event: CaptureEvent): boolean {
  const lower = text.toLowerCase();
  switch (event.kind) {
    case 'console': {
      const p = event.payload as ConsolePayload;
      return includesText(lower, p.text) && !namesWrongOption(lower, CONSOLE_LEVELS, p.level);
    }
    case 'network': {
      const p = event.payload as NetworkPayload;
      if (!includesText(lower, p.url)) return false;
      // Strip the URL before checking the method/status — a path segment
      // like "/api/get-report" or "/patients/123" must never be mistaken
      // for a claimed method or status code.
      const withoutUrl = lower.split(p.url.toLowerCase()).join(' ');
      return !namesWrongOption(withoutUrl, NETWORK_METHODS, p.method) && !namesWrongStatus(lower, p.url, p.status);
    }
    case 'interaction': {
      const p = event.payload as InteractionPayload;
      return (
        (includesText(lower, p.targetName) || includesText(lower, p.url)) &&
        !namesWrongOption(lower, Object.values(INTERACTION_VERBS), INTERACTION_VERBS[p.type])
      );
    }
    case 'navigation': {
      const p = event.payload as NavigationPayload;
      return includesText(lower, p.to) && !namesWrongOption(lower, NAVIGATION_TRIGGERS, p.trigger);
    }
    case 'lifecycle': {
      const p = event.payload as LifecyclePayload;
      return includesText(lower, p.transition);
    }
    case 'annotation': {
      const p = event.payload as AnnotationPayload;
      return includesText(lower, p.text);
    }
  }
}

function isValidCandidate(candidate: StepCandidate, eventsById: Map<string, CaptureEvent>): boolean {
  if (candidate.eventIds.length === 0) return false;

  const citedEvents: CaptureEvent[] = [];
  for (const id of candidate.eventIds) {
    const event = eventsById.get(id);
    if (event === undefined) return false; // fabricated event ID
    citedEvents.push(event);
  }

  return citedEvents.every((event) => describesEvent(candidate.text, event));
}

/**
 * Adds LLM-authored steps on top of the deterministic floor. Deterministic
 * steps are always kept as-is. Every candidate step is validated against the
 * real timeline before it survives; a step that fails is dropped
 * individually, with no partial-failure threshold. If nothing survives, the
 * LLM pass is discarded entirely and the deterministic document is returned
 * unchanged. Any provider failure (timeout, network error, malformed JSON)
 * is non-fatal and also yields the deterministic document.
 */
export async function enrichDoc({
  doc,
  events,
  provider,
  timeoutMs = 10_000,
}: EnrichDocInput): Promise<ReplicationDoc> {
  if (!isLocalProvider(provider)) return doc;

  let raw: string;
  try {
    raw = await provider.complete({
      prompt: buildPrompt(doc, events),
      timeoutMs,
    });
  } catch {
    return doc;
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return doc;
  }

  if (!Array.isArray(parsed)) return doc;

  const eventsById = new Map(events.map((event) => [event.id, event]));
  const survivors = parsed.filter(isStepCandidate).filter((c) => isValidCandidate(c, eventsById));

  if (survivors.length === 0) return doc;

  const lastN = Math.max(0, ...doc.steps.map((s) => s.n));
  const steps: Step[] = [
    ...doc.steps,
    ...survivors.map(
      (candidate, index): Step => ({
        n: lastN + index + 1,
        text: candidate.text,
        eventIds: candidate.eventIds,
        tVideo: null,
      }),
    ),
  ];

  return {
    ...doc,
    steps,
    generator: 'llm',
    generatorModel: provider.name,
  };
}

/**
 * Only the fields `describesEvent` actually validates against — not the raw
 * event. `NetworkPayload` in particular carries request/response headers and
 * bodies that no validation reads; sending them to a provider that may
 * legitimately be remote is disclosure with no corresponding benefit.
 */
function summarizeEvent(event: CaptureEvent): Record<string, unknown> {
  const base = { id: event.id, t: event.t, kind: event.kind };
  switch (event.kind) {
    case 'console': {
      const p = event.payload as ConsolePayload;
      return { ...base, level: p.level, text: p.text };
    }
    case 'network': {
      const p = event.payload as NetworkPayload;
      return { ...base, method: p.method, url: p.url, status: p.status };
    }
    case 'interaction': {
      const p = event.payload as InteractionPayload;
      return { ...base, type: p.type, targetName: p.targetName, url: p.url };
    }
    case 'navigation': {
      const p = event.payload as NavigationPayload;
      return { ...base, to: p.to, trigger: p.trigger };
    }
    case 'lifecycle': {
      const p = event.payload as LifecyclePayload;
      return { ...base, transition: p.transition };
    }
    case 'annotation': {
      const p = event.payload as AnnotationPayload;
      return { ...base, text: p.text };
    }
  }
}

function buildPrompt(doc: ReplicationDoc, events: CaptureEvent[]): string {
  return JSON.stringify({
    instructions:
      'Return a JSON array of additional repro steps as {text, eventIds}. Every eventIds entry must be one of the ids listed in "events". Do not restate the deterministic steps.',
    deterministicSteps: doc.steps,
    events: events.map(summarizeEvent),
  });
}
