import type { Step } from '@htr/capture-core';

/**
 * Each step is clickable — clicking scrubs the video to `tVideo` and
 * highlights the events it cites. Steps with no `tVideo` (e.g. a step
 * derived purely from console/network evidence with no matching frame)
 * still render and are still selectable; only the scrub is skipped.
 */
export function Steps({
  steps,
  selectedStepN,
  onSelect,
}: {
  steps: Step[];
  selectedStepN: number | null;
  onSelect: (step: Step) => void;
}) {
  return (
    <ol className="steps">
      {steps.map((step) => (
        <li key={step.n}>
          <button
            type="button"
            className={step.n === selectedStepN ? 'steps__item steps__item--selected' : 'steps__item'}
            aria-current={step.n === selectedStepN}
            onClick={() => {
              onSelect(step);
            }}
          >
            <span className="steps__n">{step.n}</span>
            <span className="steps__text">{step.text}</span>
            {step.tVideo === null && <span className="steps__no-video">no video</span>}
          </button>
        </li>
      ))}
    </ol>
  );
}
