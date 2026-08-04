import { newId, targetName, type CaptureEvent, type ElementDescriptor, type InteractionPayload } from '@htr/capture-core';
import type { Clock } from '@htr/capture-core';
import type { ElementLike } from '../lib/dom-types.js';

const LABEL_SELECTOR = 'label';

function nullIfEmpty(value: string | null): string | null {
  if (value === null) return null;
  return value.length === 0 ? null : value;
}

/**
 * Build a plain, serializable `ElementDescriptor` for `el`. Never returns or
 * touches the live node again after this call — callers send the descriptor,
 * `capture-core`'s naming step never sees a DOM element.
 */
export function describeElement(el: ElementLike): ElementDescriptor {
  const labelledBy = el.closest(LABEL_SELECTOR);
  return {
    accessibleName: nullIfEmpty(el.getAttribute('aria-label')),
    labelText: labelledBy ? nullIfEmpty(labelledBy.textContent) : null,
    textContent: el.textContent,
    testId: nullIfEmpty(el.getAttribute('data-testid')),
    selector: buildSelector(el),
  };
}

/** Best-effort CSS selector: data-testid > id > tag.first-class > tag. */
export function buildSelector(el: ElementLike): string {
  const testId = el.getAttribute('data-testid');
  if (testId) return `[data-testid="${testId}"]`;

  const id = el.getAttribute('id');
  if (id) return `#${id}`;

  const tag = el.tagName.toLowerCase();
  const className = el.getAttribute('class');
  const firstClass = className?.trim().split(/\s+/)[0];
  return firstClass ? `${tag}.${firstClass}` : tag;
}

export type InteractionSourceEvent = {
  type: InteractionPayload['type'];
  target: ElementLike;
  url: string;
  value: string | null;
};

/**
 * Turn a raw DOM interaction into a `CaptureEvent`. `t` comes from `clock`,
 * per the project convention of epoch-relative offsets, never wall time.
 */
export function buildInteractionEvent(
  source: InteractionSourceEvent,
  captureId: string,
  clock: Clock,
): CaptureEvent {
  const descriptor = describeElement(source.target);
  const payload: InteractionPayload = {
    type: source.type,
    targetName: targetName(descriptor),
    targetSelector: descriptor.selector,
    url: source.url,
    value: source.value,
  };
  return {
    id: newId(),
    captureId,
    t: clock.now(),
    kind: 'interaction',
    payload,
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

export type DomEventLike = { target: unknown };

export type DocumentLike = {
  addEventListener(type: string, handler: (event: DomEventLike) => void): void;
  removeEventListener(type: string, handler: (event: DomEventLike) => void): void;
};

const TRACKED_TYPES: InteractionPayload['type'][] = [
  'click',
  'input',
  'keydown',
  'scroll',
  'submit',
];

function isElementLike(value: unknown): value is ElementLike {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as ElementLike).getAttribute === 'function'
  );
}

function readValue(target: unknown): string | null {
  if (typeof target === 'object' && target !== null && 'value' in target) {
    return typeof target.value === 'string' ? target.value : null;
  }
  return null;
}

/**
 * Wire the interaction trail: one capturing listener per tracked event type,
 * each turning the DOM event into a descriptor-bearing `CaptureEvent` and
 * handing it to `emit`. Returns a teardown function that removes every
 * listener it added — callers gate this on an active capture (there is no
 * captureId to attribute events to otherwise) and must tear it down on stop.
 */
export function startInteractionTrail(
  doc: DocumentLike,
  captureId: string,
  clock: Clock,
  urlProvider: () => string,
  emit: (event: CaptureEvent) => void,
): () => void {
  const teardowns: (() => void)[] = [];
  for (const type of TRACKED_TYPES) {
    const handler = (event: DomEventLike): void => {
      if (!isElementLike(event.target)) return;
      emit(
        buildInteractionEvent(
          { type, target: event.target, url: urlProvider(), value: readValue(event.target) },
          captureId,
          clock,
        ),
      );
    };
    doc.addEventListener(type, handler);
    teardowns.push(() => doc.removeEventListener(type, handler));
  }
  return (): void => {
    for (const teardown of teardowns) teardown();
  };
}
