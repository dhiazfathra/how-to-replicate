import type { AssetRef } from '../types/asset.js';
import type { ReplicationDoc, Step } from '../types/doc.js';
import type {
  CaptureEvent,
  ConsolePayload,
  InteractionPayload,
  NavigationPayload,
} from '../types/event.js';
import { reduceNoise } from './noise.js';

export type GenerateDocOptions = {
  assets?: AssetRef[];
};

type Candidate = {
  t: number;
  text: string;
  eventIds: string[];
};

/**
 * Renders each non-`mousemove` interaction type as mechanical prose.
 * `mousemove` has no renderer: `reduceNoise` drops it before this table is
 * consulted, so it never needs one.
 */
const INTERACTION_TEXT: Record<
  Exclude<InteractionPayload['type'], 'mousemove'>,
  (payload: InteractionPayload, name: string, count: number) => string
> = {
  click: (payload, name, count) =>
    `Clicked ${name} on ${payload.url}${count > 1 ? ` ${count} times` : ''}`,
  input: (payload, name) => `Typed into ${name} on ${payload.url}`,
  // The `''` fallback below is exercised by generate.test.ts's null-value
  // keydown fixture (asserts the exact `Pressed "" on ...` output), but the
  // v8 coverage provider does not register it as taken from inside a
  // template-literal interpolation on an object-literal arrow's implicit
  // return — hence the ignore hint on the next line.
  keydown: (payload) => {
    /* v8 ignore next */
    const key = payload.value ?? '';
    return `Pressed "${key}" on ${payload.url}`;
  },
  submit: (payload, name) => `Submitted ${name} on ${payload.url}`,
  scroll: (payload) => `Scrolled on ${payload.url}`,
};

function describeInteraction(events: CaptureEvent[]): Candidate {
  const first = events[0] as CaptureEvent;
  const payload = first.payload as InteractionPayload;
  const eventIds = events.map((e) => e.id);
  const name = JSON.stringify(payload.targetName);
  const render =
    INTERACTION_TEXT[payload.type as Exclude<InteractionPayload['type'], 'mousemove'>];

  return {
    t: first.t,
    text: render(payload, name, events.length),
    eventIds,
  };
}

function describeNavigation(event: CaptureEvent): Candidate {
  const payload = event.payload as NavigationPayload;
  return {
    t: event.t,
    text: `Navigated to ${payload.to}`,
    eventIds: [event.id],
  };
}

function resolveTitle(events: CaptureEvent[]): string {
  const firstError = events.find(
    (e) => e.kind === 'console' && (e.payload as ConsolePayload).level === 'error',
  );
  if (firstError) {
    return (firstError.payload as ConsolePayload).text;
  }

  const navigations = events.filter((e) => e.kind === 'navigation');
  const lastNavigation = navigations[navigations.length - 1];
  if (lastNavigation) {
    return `Navigate to ${(lastNavigation.payload as NavigationPayload).to}`;
  }

  return 'Untitled capture';
}

/**
 * Build the deterministic floor of a replication doc from a capture's raw
 * timeline: no LLM, no network, always available. Interaction events are
 * noise-reduced (`reduceNoise`) and rendered as mechanical prose; navigation
 * events become their own steps. Steps are ordered by `t`.
 */
export function generateDoc(
  events: CaptureEvent[],
  opts: GenerateDocOptions = {},
): ReplicationDoc {
  const interactionGroups = reduceNoise(
    events.filter((e) => e.kind === 'interaction'),
  );
  const navigationEvents = events.filter((e) => e.kind === 'navigation');

  const candidates = [
    ...interactionGroups.map((g) => describeInteraction(g.events)),
    ...navigationEvents.map(describeNavigation),
  ].sort((a, b) => a.t - b.t);

  const hasVideo = (opts.assets ?? []).some((a) => a.kind === 'video');

  const steps: Step[] = candidates.map((c, index) => ({
    n: index + 1,
    text: c.text,
    eventIds: c.eventIds,
    tVideo: hasVideo ? c.t : null,
  }));

  return {
    title: resolveTitle(events),
    summary: `${steps.length} step(s) recorded deterministically.`,
    steps,
    expected: null,
    actual: null,
    generator: 'deterministic',
    generatorModel: null,
  };
}
