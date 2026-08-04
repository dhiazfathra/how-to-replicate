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
import type { LlmProvider } from './provider.js';

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
      return includesText(lower, p.text);
    }
    case 'network': {
      const p = event.payload as NetworkPayload;
      return includesText(lower, p.url);
    }
    case 'interaction': {
      const p = event.payload as InteractionPayload;
      return includesText(lower, p.targetName) || includesText(lower, p.url);
    }
    case 'navigation': {
      const p = event.payload as NavigationPayload;
      return includesText(lower, p.to);
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

  const steps: Step[] = [
    ...doc.steps,
    ...survivors.map(
      (candidate, index): Step => ({
        n: doc.steps.length + index + 1,
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

function buildPrompt(doc: ReplicationDoc, events: CaptureEvent[]): string {
  return JSON.stringify({
    instructions:
      'Return a JSON array of additional repro steps as {text, eventIds}. Every eventIds entry must be one of the ids listed in "events". Do not restate the deterministic steps.',
    deterministicSteps: doc.steps,
    events,
  });
}
