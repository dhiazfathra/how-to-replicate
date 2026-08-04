import type { CaptureEvent } from '@htr/capture-core';

const KINDS = ['console', 'network', 'interaction', 'navigation', 'lifecycle', 'annotation'] as const;

export function Timeline({
  events,
  highlightedEventIds = [],
  kind,
  onKindChange,
}: {
  events: CaptureEvent[];
  highlightedEventIds?: string[];
  kind: CaptureEvent['kind'] | 'all';
  onKindChange: (kind: CaptureEvent['kind'] | 'all') => void;
}) {
  const filtered = kind === 'all' ? events : events.filter((e) => e.kind === kind);
  const highlighted = new Set(highlightedEventIds);

  return (
    <div className="timeline">
      <div className="timeline__filters" role="group" aria-label="Filter by kind">
        <button
          type="button"
          aria-pressed={kind === 'all'}
          onClick={() => {
            onKindChange('all');
          }}
        >
          all
        </button>
        {KINDS.map((k) => (
          <button
            key={k}
            type="button"
            aria-pressed={kind === k}
            onClick={() => {
              onKindChange(k);
            }}
          >
            {k}
          </button>
        ))}
      </div>
      <ol className="timeline__events">
        {filtered.map((event) => (
          <li
            key={event.id}
            data-event-id={event.id}
            className={highlighted.has(event.id) ? 'timeline__event timeline__event--highlighted' : 'timeline__event'}
          >
            <span className="timeline__event-t">{event.t}ms</span>
            <span className="timeline__event-kind">{event.kind}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}
