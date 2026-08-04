import type { CaptureEvent, InteractionPayload } from '../types/event.js';

const SCROLL_COALESCE_WINDOW_MS = 500;
const CLICK_FOLD_WINDOW_MS = 1000;

/** A run of one or more interaction events collapsed into a single step. */
export type InteractionGroup = {
  events: CaptureEvent[];
};

function interactionPayload(event: CaptureEvent): InteractionPayload {
  return event.payload as InteractionPayload;
}

function lastEvent(group: InteractionGroup): CaptureEvent {
  return group.events[group.events.length - 1] as CaptureEvent;
}

/**
 * Apply the deterministic noise-reduction rules to a timeline's interaction
 * events (already filtered to `kind === 'interaction'`, in ascending `t`
 * order): `mousemove` is dropped entirely, consecutive `scroll` events within
 * 500ms coalesce into one group, and identical consecutive `click`s (same
 * `targetSelector`) within 1000ms fold into one group.
 */
export function reduceNoise(events: CaptureEvent[]): InteractionGroup[] {
  const groups: InteractionGroup[] = [];

  for (const event of events) {
    const payload = interactionPayload(event);
    if (payload.type === 'mousemove') continue;

    const prev = groups[groups.length - 1];
    if (prev) {
      const prevPayload = interactionPayload(lastEvent(prev));
      const dt = event.t - lastEvent(prev).t;
      const isCoalescableScroll =
        payload.type === 'scroll' &&
        prevPayload.type === 'scroll' &&
        dt <= SCROLL_COALESCE_WINDOW_MS;
      const isFoldableClick =
        payload.type === 'click' &&
        prevPayload.type === 'click' &&
        prevPayload.targetSelector === payload.targetSelector &&
        dt <= CLICK_FOLD_WINDOW_MS;
      if (isCoalescableScroll || isFoldableClick) {
        prev.events.push(event);
        continue;
      }
    }
    groups.push({ events: [event] });
  }

  return groups;
}
