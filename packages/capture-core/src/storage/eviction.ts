import type { Capture } from '../types/capture.js';
import type { CaptureRepository } from './repository.js';
import type { CheckBudgetResult } from './budget.js';

/**
 * Eviction eligibility (invariant 8): only a capture whose manifest has been
 * fully verified server-side may lose its local copy. `lastPushedAt` is a
 * push timestamp, not a completion signal — a capture can push metadata
 * repeatedly while its video is still uploading, so it is never consulted
 * for eligibility, only for LRU ordering among already-eligible captures.
 */
function isEvictable(capture: Capture): boolean {
  return capture.sync.manifestComplete === true && capture.localAssets !== false;
}

function pushedAtMillis(capture: Capture): number {
  // Eligible captures should always have a lastPushedAt, but fall back to
  // createdAt defensively rather than crash on unexpected data.
  const ts = capture.sync.lastPushedAt ?? capture.createdAt;
  return new Date(ts).getTime();
}

/**
 * Picks the least-recently-pushed evictable capture, or undefined if nothing
 * qualifies. Never falls back to an ineligible capture.
 */
export function selectEvictionCandidate(captures: Capture[]): Capture | undefined {
  const evictable = captures.filter(isEvictable);
  if (evictable.length === 0) return undefined;

  return evictable.reduce((oldest, candidate) =>
    pushedAtMillis(candidate) < pushedAtMillis(oldest) ? candidate : oldest,
  );
}

export type EvictionResult = { evictedCaptureId: string } | { evictedCaptureId: null };

/**
 * Runs under storage pressure (a failed checkBudget). If a capture is
 * evictable, removes its local events/assets and returns its id. If nothing
 * is evictable — including when sync isn't configured at all — this is a
 * no-op: the caller falls through to the Phase 0 refuse-to-record /
 * export-or-delete prompt. Eviction never substitutes for that refusal.
 */
export async function runEviction(
  repository: CaptureRepository,
  captures: Capture[],
  budgetResult: CheckBudgetResult,
): Promise<EvictionResult> {
  if (budgetResult.ok) return { evictedCaptureId: null };

  const candidate = selectEvictionCandidate(captures);
  if (!candidate) return { evictedCaptureId: null };

  await repository.evictLocalAssets(candidate.id);
  return { evictedCaptureId: candidate.id };
}
