import { useEffect, useMemo, useState } from 'react';
import { isViewable, type Capture, type CaptureEvent, type CaptureStore } from '@htr/capture-core';
import { useCapture, useCaptureIds } from './store/bindings.js';
import { CaptureDetail } from './CaptureDetail.js';
import { CommandPalette, type PaletteAction } from './components/CommandPalette.js';

export type ViewerRepo = {
  readEvents(captureId: string): Promise<CaptureEvent[]>;
  readAsset(assetId: string): Promise<Uint8Array | undefined>;
};

/** Route is app-local state, not a URL — Phase 0 has no router dependency to add. */
type Route = { name: 'list' } | { name: 'capture'; id: string };

export function App({ store, repo }: { store: CaptureStore; repo: ViewerRepo }) {
  const [route, setRoute] = useState<Route>({ name: 'list' });
  const [paletteOpen, setPaletteOpen] = useState(false);
  const ids = useCaptureIds(store);

  useEffect(() => {
    function onKeydown(e: KeyboardEvent): void {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen((open) => !open);
      }
    }
    window.addEventListener('keydown', onKeydown);
    return () => {
      window.removeEventListener('keydown', onKeydown);
    };
  }, []);

  const captures = ids
    .map((id) => store.capture(id).get())
    .filter((c): c is Capture => c !== undefined);

  const actions: PaletteAction[] = useMemo(
    () => [{ id: 'go-to-list', label: 'Go to capture list', run: () => { setRoute({ name: 'list' }); } }],
    [],
  );

  return (
    <div className="app">
      {route.name === 'list' ? (
        <CaptureList
          captures={captures}
          onOpen={(id) => {
            setRoute({ name: 'capture', id });
          }}
        />
      ) : (
        <CaptureDetailBound store={store} repo={repo} captureId={route.id} />
      )}
      <CommandPalette
        open={paletteOpen}
        captures={captures}
        actions={actions}
        onNavigateCapture={(id) => {
          setRoute({ name: 'capture', id });
        }}
        onNavigateProject={() => {
          setRoute({ name: 'list' });
        }}
        onClose={() => {
          setPaletteOpen(false);
        }}
      />
    </div>
  );
}

function CaptureList({
  captures,
  onOpen,
}: {
  captures: Capture[];
  onOpen: (id: string) => void;
}) {
  return (
    <ul className="capture-list">
      {captures.map((capture) => (
        <li key={capture.id}>
          <button
            type="button"
            onClick={() => {
              onOpen(capture.id);
            }}
          >
            {capture.doc?.title ?? capture.id}
            {!isViewable(capture) && <span className="capture-list__state"> ({capture.state})</span>}
          </button>
        </li>
      ))}
    </ul>
  );
}

function CaptureDetailBound({
  store,
  repo,
  captureId,
}: {
  store: CaptureStore;
  repo: ViewerRepo;
  captureId: string;
}) {
  const capture = useCapture(store, captureId);

  // Invariant 1: a capture that is not `ready` renders nothing of its
  // content — not the doc, not the timeline, not the player, not the assets.
  if (!capture || !isViewable(capture)) {
    return (
      <div className="capture-detail capture-detail--not-ready">
        Capture is not ready{capture ? ` (state: ${capture.state})` : ''}.
      </div>
    );
  }

  return <CaptureDetail capture={capture} repo={repo} />;
}
