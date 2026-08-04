import type { Capture, CaptureState } from '../types/capture.js';
import type { CaptureEvent } from '../types/event.js';
import { newId } from '../identity.js';

/** spec §6 transition table. Every state not listed here is illegal. */
const TRANSITIONS: Record<CaptureState, ReadonlySet<CaptureState>> = {
  recording: new Set(['redacting']),
  redacting: new Set(['composing', 'failed']),
  composing: new Set(['ready', 'failed']),
  ready: new Set(['expired']),
  failed: new Set(),
  expired: new Set(),
};

export type TransitionResult = {
  capture: Capture;
  event: CaptureEvent;
};

/**
 * Apply a state transition to `capture`, returning the updated capture and
 * the lifecycle event recorded for it. Throws on any transition not in the
 * spec §6 table — there is no silent no-op path.
 */
export function transition(
  capture: Capture,
  to: CaptureState,
  detail: string | null = null,
): TransitionResult {
  const allowed = TRANSITIONS[capture.state];
  if (!allowed.has(to)) {
    throw new Error(`illegal capture transition: ${capture.state} -> ${to}`);
  }

  const event: CaptureEvent = {
    id: newId(),
    captureId: capture.id,
    t: 0,
    kind: 'lifecycle',
    payload: { transition: `${capture.state}->${to}`, detail },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };

  return { capture: { ...capture, state: to }, event };
}
