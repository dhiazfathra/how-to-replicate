import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { act } from 'react';
import { createRecordStore } from '@htr/capture-core';
import type { Capture, CaptureStore } from '@htr/capture-core';
import { useCapture, useCaptureIds, usePersistError } from './bindings.js';

function makeStore(captures: Capture[]): CaptureStore {
  const records = createRecordStore<Capture | undefined>();
  const errors = createRecordStore<Error | undefined>();
  const order = captures.map((c) => c.id);
  for (const c of captures) records.field(c.id, 'capture', undefined).set(c);

  return {
    async hydrate() {},
    ids: () => [...order],
    capture: (id) => records.field(id, 'capture', undefined),
    async setField(id, field, value) {
      const obs = records.field(id, 'capture', undefined);
      const current = obs.get();
      if (current) obs.set({ ...current, [field]: value });
    },
    persistError: (id, field) => errors.field(id, String(field), undefined),
    putLocal: (c) => {
      records.field(c.id, 'capture', undefined).set(c);
    },
  };
}

function makeCapture(id: string, overrides: Partial<Capture> = {}): Capture {
  return {
    id,
    workspaceId: null,
    projectId: null,
    source: 'extension',
    state: 'ready',
    fidelity: 'full',
    createdAt: '2026-08-04T00:00:00.000Z',
    epoch: 0,
    env: {
      userAgent: 'ua',
      platform: 'p',
      viewport: { w: 0, h: 0 },
      devicePixelRatio: 1,
      locale: 'en',
      timezone: 'UTC',
      url: 'https://example.com',
    },
    metadata: {},
    doc: null,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: true, dirtyFields: [] },
    ...overrides,
  };
}

function Probe({ store, id }: { store: CaptureStore; id: string }) {
  const capture = useCapture(store, id);
  return <div data-testid={`probe-${id}`}>{capture?.state ?? 'none'}</div>;
}

describe('bindings', () => {
  it('useCapture returns the current value and re-renders on set', () => {
    const store = makeStore([makeCapture('a'), makeCapture('b')]);
    render(
      <>
        <Probe store={store} id="a" />
        <Probe store={store} id="b" />
      </>,
    );
    expect(screen.getByTestId('probe-a').textContent).toBe('ready');
    expect(screen.getByTestId('probe-b').textContent).toBe('ready');

    act(() => {
      void store.setField('a', 'state', 'recording');
    });

    expect(screen.getByTestId('probe-a').textContent).toBe('recording');
    // capture b's cell was never touched — per-field granularity.
    expect(screen.getByTestId('probe-b').textContent).toBe('ready');
  });

  it('useCapture returns undefined for an unknown id', () => {
    const store = makeStore([]);
    render(<Probe store={store} id="missing" />);
    expect(screen.getByTestId('probe-missing').textContent).toBe('none');
  });

  it('useCaptureIds returns the store snapshot', () => {
    const store = makeStore([makeCapture('a'), makeCapture('b')]);
    expect(useCaptureIdsDirect(store)).toEqual(['a', 'b']);
  });

  function useCaptureIdsDirect(store: CaptureStore): string[] {
    // Exercised via a throwaway component to stay inside React's render cycle.
    let result: string[] = [];
    function Read() {
      result = useCaptureIds(store);
      return null;
    }
    render(<Read />);
    return result;
  }

  it('usePersistError returns the current error observable value', () => {
    const store = makeStore([makeCapture('a')]);
    function Read() {
      const err = usePersistError(store, 'a', 'state');
      return <div data-testid="err">{err?.message ?? 'none'}</div>;
    }
    render(<Read />);
    expect(screen.getByTestId('err').textContent).toBe('none');
  });
});
