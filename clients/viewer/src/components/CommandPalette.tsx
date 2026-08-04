import { useMemo, useState } from 'react';
import type { Capture } from '@htr/capture-core';

export type PaletteAction = { id: string; label: string; run: () => void };

export type PaletteResult =
  | { type: 'capture'; id: string; label: string }
  | { type: 'project'; id: string; label: string }
  | { type: 'action'; id: string; label: string; run: () => void };

/**
 * Searches only what's already resident in the local store — `captures` and
 * `actions` are passed in as props, never fetched here. This component must
 * never call `fetch`/`XMLHttpRequest`; a test asserts that.
 */
export function CommandPalette({
  open,
  captures,
  actions,
  onNavigateCapture,
  onNavigateProject,
  onClose,
}: {
  open: boolean;
  captures: Capture[];
  actions: PaletteAction[];
  onNavigateCapture: (id: string) => void;
  onNavigateProject: (id: string) => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState('');

  const results = useMemo<PaletteResult[]>(() => {
    const q = query.trim().toLowerCase();
    const projectIds = [...new Set(captures.map((c) => c.projectId).filter((id): id is string => id !== null))];

    const captureResults: PaletteResult[] = captures
      .filter((c) => q === '' || c.id.toLowerCase().includes(q) || (c.doc?.title ?? '').toLowerCase().includes(q))
      .map((c) => ({ type: 'capture', id: c.id, label: c.doc?.title ?? c.id }));

    const projectResults: PaletteResult[] = projectIds
      .filter((id) => q === '' || id.toLowerCase().includes(q))
      .map((id) => ({ type: 'project', id, label: id }));

    const actionResults: PaletteResult[] = actions
      .filter((a) => q === '' || a.label.toLowerCase().includes(q))
      .map((a) => ({ type: 'action', id: a.id, label: a.label, run: a.run }));

    return [...captureResults, ...projectResults, ...actionResults];
  }, [query, captures, actions]);

  if (!open) return null;

  function select(result: PaletteResult): void {
    if (result.type === 'capture') onNavigateCapture(result.id);
    else if (result.type === 'project') onNavigateProject(result.id);
    else result.run();
    onClose();
  }

  return (
    <div className="command-palette" role="dialog" aria-label="Command palette">
      <input
        autoFocus
        type="text"
        aria-label="Search"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
        }}
      />
      <ul className="command-palette__results">
        {results.map((result) => (
          <li key={`${result.type}:${result.id}`}>
            <button
              type="button"
              onClick={() => {
                select(result);
              }}
            >
              <span className="command-palette__kind">{result.type}</span>
              {result.label}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
