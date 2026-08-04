import { describe, expect, it, vi } from 'vitest';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createRecordStore } from '@htr/capture-core';
import type { Capture, CaptureEvent, CaptureStore, Step } from '@htr/capture-core';
import { App, type ViewerRepo } from './App.js';

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
    projectId: 'proj-1',
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

const step: Step = { n: 1, text: 'Click login', eventIds: ['e1'], tVideo: 1500 };

function makeRepo(events: CaptureEvent[] = []): ViewerRepo {
  return {
    readEvents: vi.fn().mockResolvedValue(events),
    readAsset: vi.fn().mockResolvedValue(undefined),
  };
}

describe('App', () => {
  it('never renders capture content for a non-ready capture', async () => {
    const user = userEvent.setup();
    const capture = makeCapture('cap-1', {
      state: 'recording',
      doc: { title: 'SECRET TITLE', summary: '', steps: [step], expected: null, actual: null, generator: 'deterministic', generatorModel: null },
    });
    const store = makeStore([capture]);
    render(<App store={store} repo={makeRepo()} />);

    await user.click(screen.getByRole('button', { name: /cap-1/ }));

    expect(screen.getByText(/not ready/)).toBeInTheDocument();
    expect(screen.queryByText('Click login')).not.toBeInTheDocument();
    expect(screen.queryByText('SECRET TITLE')).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain('SECRET TITLE');
  });

  it('never leaks a doc title into the capture list for a non-ready capture (e.g. ready -> expired)', () => {
    // Regression test: machine.ts allows ready -> expired, and transition()
    // spreads ...capture, so an expired capture keeps its non-null `doc`.
    // The capture list must not render doc-derived content for it.
    const capture = makeCapture('cap-1', {
      state: 'expired',
      doc: { title: 'EXPIRED SECRET TITLE', summary: '', steps: [step], expected: null, actual: null, generator: 'deterministic', generatorModel: null },
    });
    const store = makeStore([capture]);
    render(<App store={store} repo={makeRepo()} />);

    expect(screen.queryByText('EXPIRED SECRET TITLE')).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain('EXPIRED SECRET TITLE');
    expect(screen.getByRole('button', { name: /cap-1/ })).toBeInTheDocument();
  });

  it('renders capture content for a ready capture', async () => {
    const user = userEvent.setup();
    const capture = makeCapture('cap-1', {
      doc: { title: 'Login bug', summary: '', steps: [step], expected: null, actual: null, generator: 'deterministic', generatorModel: null },
    });
    const store = makeStore([capture]);
    render(<App store={store} repo={makeRepo()} />);

    await user.click(screen.getByRole('button', { name: /Login bug/ }));

    expect(await screen.findByText('Click login')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Login bug' })).toBeInTheDocument();
  });

  it('shows a not-ready message for an unknown capture id', () => {
    const store = makeStore([]);
    render(<App store={store} repo={makeRepo()} />);
    expect(screen.queryByText(/not ready/)).not.toBeInTheDocument();
  });

  it('shows a bare not-ready message (no state suffix) when the capture was never hydrated', async () => {
    const user = userEvent.setup();
    // A capture the palette can navigate to, but whose store cell is empty —
    // covers the `capture ? … : ''` falsy branch distinct from "exists but
    // not ready".
    const capture = makeCapture('ghost', { doc: { title: 'Ghost capture', summary: '', steps: [], expected: null, actual: null, generator: 'deterministic', generatorModel: null } });
    const store = makeStore([capture]);
    render(<App store={store} repo={makeRepo()} />);

    await user.keyboard('{Meta>}k{/Meta}');
    const dialog = screen.getByRole('dialog', { name: 'Command palette' });
    await user.click(within(dialog).getByText('Ghost capture'));
    expect(await screen.findByRole('heading', { name: 'Ghost capture' })).toBeInTheDocument();

    // Simulate the capture disappearing from the store after navigation
    // (e.g. deleted) — the bound view must re-render to the not-ready state.
    act(() => {
      store.capture('ghost').set(undefined);
    });

    const message = screen.getByText(/Capture is not ready/);
    expect(message.textContent).toBe('Capture is not ready.');
  });

  it('opens the command palette on cmd+k and navigates to a capture', async () => {
    const capture = makeCapture('cap-1', { doc: { title: 'Login bug', summary: '', steps: [], expected: null, actual: null, generator: 'deterministic', generatorModel: null } });
    const store = makeStore([capture]);
    render(<App store={store} repo={makeRepo()} />);

    const user = userEvent.setup();
    await user.keyboard('{Meta>}k{/Meta}');
    const dialog = screen.getByRole('dialog', { name: 'Command palette' });

    await user.click(within(dialog).getByText('Login bug'));
    // The palette fades out over 150ms before unmounting rather than vanishing instantly.
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    expect(await screen.findByRole('heading', { name: 'Login bug' })).toBeInTheDocument();
  });

  it('navigating to a project from the palette returns to the capture list', async () => {
    const capture = makeCapture('cap-1', { doc: { title: 'Login bug', summary: '', steps: [], expected: null, actual: null, generator: 'deterministic', generatorModel: null } });
    const store = makeStore([capture]);
    render(<App store={store} repo={makeRepo()} />);

    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: /Login bug/ }));
    expect(await screen.findByRole('heading', { name: 'Login bug' })).toBeInTheDocument();

    await user.keyboard('{Meta>}k{/Meta}');
    const dialog = screen.getByRole('dialog', { name: 'Command palette' });
    await user.click(within(dialog).getByText('proj-1'));

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    expect(screen.queryByRole('heading', { name: 'Login bug' })).not.toBeInTheDocument();
  });

  it('toggles the command palette closed on a second cmd+k', async () => {
    const store = makeStore([makeCapture('cap-1')]);
    render(<App store={store} repo={makeRepo()} />);
    const user = userEvent.setup();
    await user.keyboard('{Meta>}k{/Meta}');
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    await user.keyboard('{Meta>}k{/Meta}');
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  it('opens the command palette on ctrl+k too', async () => {
    const store = makeStore([makeCapture('cap-1')]);
    render(<App store={store} repo={makeRepo()} />);
    const user = userEvent.setup();
    await user.keyboard('{Control>}k{/Control}');
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('ignores keydowns that are not the palette shortcut', async () => {
    const store = makeStore([makeCapture('cap-1')]);
    render(<App store={store} repo={makeRepo()} />);
    const user = userEvent.setup();
    await user.keyboard('{Meta>}j{/Meta}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.keyboard('k');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('runs the "go to capture list" palette action from a capture detail view', async () => {
    const user = userEvent.setup();
    const capture = makeCapture('cap-1', { doc: { title: 'Login bug', summary: '', steps: [], expected: null, actual: null, generator: 'deterministic', generatorModel: null } });
    const store = makeStore([capture]);
    render(<App store={store} repo={makeRepo()} />);

    await user.click(screen.getByRole('button', { name: /Login bug/ }));
    expect(await screen.findByRole('heading', { name: 'Login bug' })).toBeInTheDocument();

    await user.keyboard('{Meta>}k{/Meta}');
    const dialog = screen.getByRole('dialog', { name: 'Command palette' });
    await user.click(within(dialog).getByText('Go to capture list'));

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    expect(screen.queryByRole('heading', { name: 'Login bug' })).not.toBeInTheDocument();
  });

  it('step selection scrubs the player and highlights cited events', async () => {
    const user = userEvent.setup();
    const captureEvent: CaptureEvent = {
      id: 'e1',
      captureId: 'cap-1',
      t: 1500,
      kind: 'interaction',
      payload: { type: 'click', targetName: 'login', targetSelector: '#login', url: 'https://x', value: null },
      redaction: { rulesApplied: [], fidelity: 'full' },
    };
    const capture = makeCapture('cap-1', {
      doc: { title: 'Login bug', summary: '', steps: [step], expected: null, actual: null, generator: 'deterministic', generatorModel: null },
    });
    const store = makeStore([capture]);
    render(<App store={store} repo={makeRepo([captureEvent])} />);

    await user.click(screen.getByRole('button', { name: /Login bug/ }));
    await user.click(await screen.findByRole('button', { name: /Click login/ }));

    const highlightedEvent = await screen.findByText('interaction', {
      selector: '.timeline__event--highlighted .timeline__event-kind',
    });
    expect(highlightedEvent).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Click login/ })).toHaveAttribute('aria-current', 'true');
  });
});
