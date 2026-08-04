import type { Capture } from '../types/capture.js';
import type { CaptureEvent } from '../types/event.js';
import type { GenerateDocOptions } from '../steps/generate.js';
import type { ReplicationDoc } from '../types/doc.js';
import type { InstantReplay } from '../buffer/instant-replay.js';
import type { CaptureRepository } from '../storage/repository.js';
import type { Clock } from '../time.js';
import { generateDoc } from '../steps/generate.js';
import { createClock } from '../time.js';
import { transition } from './machine.js';

export type DocGenerator = (events: CaptureEvent[], opts?: GenerateDocOptions) => ReplicationDoc;

export type FinalizeCaptureOptions = {
  capture: Capture;
  buffer: InstantReplay;
  repo: CaptureRepository;
  docGenerator?: DocGenerator;
  /** Source of the `t` offset stamped on lifecycle events. Defaults to a fresh `createClock()`. */
  clock?: Clock;
};

/**
 * Land a capture in `ready` (or `failed` on a fatal error). Does not
 * re-apply the ruleset: Task 6's InstantReplay redacts on ingest, so
 * `buffer.events()` already holds redacted events — a second redaction pass
 * here would be redundant and a second place for the two passes to disagree.
 *
 * A redaction *drop* (buffer.withheldEventCount()) is a successful
 * fail-closed outcome and does not stop the capture reaching `ready`. Only a
 * fatal pipeline error — persistence failure, doc-gen crash, an unreadable
 * buffer — lands the capture in `failed`, where it is not viewable.
 *
 * Ring-buffer capacity evictions (buffer.evictedCount()) drop real timeline
 * events just as surely as redaction does — a How-to-Replicate step citing an
 * evicted event's ID loses context with no visible signal otherwise. Per
 * invariant 4, both drop sources feed the same withheldEventCount/fidelity
 * computation; the capture must never report fidelity: 'full' when either
 * source dropped events.
 *
 * Requires `capture.state === 'composing'`: that's the only state from which
 * both `ready` and `failed` are legal per the machine's transition table, so
 * the catch block's `transition(capture, 'failed', ...)` is always itself
 * legal. Called from any other state, this throws immediately instead of
 * risking a second, harder-to-diagnose throw from inside error handling.
 */
export async function finalizeCapture(options: FinalizeCaptureOptions): Promise<Capture> {
  const { capture, buffer, repo } = options;
  if (capture.state !== 'composing') {
    throw new Error(
      `finalizeCapture requires a composing capture (got: ${capture.state})`,
    );
  }
  const docGenerator = options.docGenerator ?? generateDoc;
  const clock = options.clock ?? createClock();

  try {
    const events = buffer.events();
    const withheldEventCount = buffer.withheldEventCount() + buffer.evictedCount();
    await repo.appendEvents(capture.id, events);
    const doc = docGenerator(events, { assets: capture.assets });

    const composed: Capture = {
      ...capture,
      doc,
      withheldEventCount,
      fidelity: withheldEventCount > 0 ? 'degraded' : capture.fidelity,
    };

    const { capture: ready, event } = transition(composed, 'ready', null, clock.now());
    await repo.appendEvents(capture.id, [event]);
    await repo.putCapture(ready);
    return ready;
  } catch (error) {
    const { capture: failed, event } = transition(
      capture,
      'failed',
      errorMessage(error),
      clock.now(),
    );
    await repo.putCapture(failed).catch(() => undefined);
    await repo.appendEvents(capture.id, [event]).catch(() => undefined);
    return failed;
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
