import type { Capture } from '@htr/capture-core';

/**
 * Invariant 4 (lost fidelity is visible): renders nothing when fidelity is
 * `full` and nothing was withheld. Once either signal is set, the badge is
 * unmissable — it never quietly disappears into a tooltip or a muted color.
 */
export function FidelityBadge({
  fidelity,
  withheldEventCount,
}: {
  fidelity: Capture['fidelity'];
  withheldEventCount: number;
}) {
  if (fidelity !== 'degraded' && withheldEventCount === 0) return null;

  return (
    <div role="status" className="fidelity-badge fidelity-badge--degraded">
      {fidelity === 'degraded' && <span className="fidelity-badge__label">Degraded capture</span>}
      {withheldEventCount > 0 && (
        <span className="fidelity-badge__count">
          {withheldEventCount} events withheld by redaction policy
        </span>
      )}
    </div>
  );
}
