import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useState } from 'react';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { Capture } from '@htr/capture-core';
import { CommandPalette } from './CommandPalette.js';

function Wrapper({ initialOpen }: { initialOpen: boolean }) {
  const [open, setOpen] = useState(initialOpen);
  return (
    <CommandPalette
      open={open}
      captures={[]}
      actions={[]}
      onNavigateCapture={() => {}}
      onNavigateProject={() => {}}
      onClose={() => {
        setOpen(false);
      }}
    />
  );
}

function makeCapture(overrides: Partial<Capture>): Capture {
  return {
    id: 'cap-1',
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

describe('CommandPalette', () => {
  let fetchSpy: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchSpy = vi.fn(() => {
      throw new Error('CommandPalette must never call fetch');
    });
    vi.stubGlobal('fetch', fetchSpy);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders nothing when closed', () => {
    const { container } = render(
      <CommandPalette
        open={false}
        captures={[]}
        actions={[]}
        onNavigateCapture={() => {}}
        onNavigateProject={() => {}}
        onClose={() => {}}
      />,
    );
    expect(container).toBeEmptyDOMElement();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('lists captures, projects, and actions from the store with no query', () => {
    const captures = [makeCapture({ id: 'cap-1', doc: { title: 'Login bug', summary: '', steps: [], expected: null, actual: null, generator: 'deterministic', generatorModel: null } })];
    render(
      <CommandPalette
        open
        captures={captures}
        actions={[{ id: 'a1', label: 'New capture', run: () => {} }]}
        onNavigateCapture={() => {}}
        onNavigateProject={() => {}}
        onClose={() => {}}
      />,
    );
    expect(screen.getByText('Login bug')).toBeInTheDocument();
    expect(screen.getByText('proj-1')).toBeInTheDocument();
    expect(screen.getByText('New capture')).toBeInTheDocument();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('filters results by query, matching id/title/project/action label', async () => {
    const user = userEvent.setup();
    const captures = [
      makeCapture({ id: 'cap-1', doc: { title: 'Login bug', summary: '', steps: [], expected: null, actual: null, generator: 'deterministic', generatorModel: null } }),
      makeCapture({ id: 'cap-2', projectId: 'other', doc: null }),
    ];
    render(
      <CommandPalette
        open
        captures={captures}
        actions={[{ id: 'a1', label: 'New capture', run: () => {} }]}
        onNavigateCapture={() => {}}
        onNavigateProject={() => {}}
        onClose={() => {}}
      />,
    );
    await user.type(screen.getByLabelText('Search'), 'login');
    expect(screen.getByText('Login bug')).toBeInTheDocument();
    expect(screen.queryByText('cap-2')).not.toBeInTheDocument();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('navigates to a capture result and closes', async () => {
    const user = userEvent.setup();
    const onNavigateCapture = vi.fn();
    const onClose = vi.fn();
    const captures = [makeCapture({ id: 'cap-1' })];
    render(
      <CommandPalette
        open
        captures={captures}
        actions={[]}
        onNavigateCapture={onNavigateCapture}
        onNavigateProject={() => {}}
        onClose={onClose}
      />,
    );
    await user.click(screen.getByRole('button', { name: /cap-1/ }));
    expect(onNavigateCapture).toHaveBeenCalledWith('cap-1');
    expect(onClose).toHaveBeenCalled();
  });

  it('navigates to a project result and closes', async () => {
    const user = userEvent.setup();
    const onNavigateProject = vi.fn();
    const onClose = vi.fn();
    const captures = [makeCapture({ id: 'cap-1', projectId: 'proj-1' })];
    render(
      <CommandPalette
        open
        captures={captures}
        actions={[]}
        onNavigateCapture={() => {}}
        onNavigateProject={onNavigateProject}
        onClose={onClose}
      />,
    );
    await user.click(screen.getByRole('button', { name: /proj-1/ }));
    expect(onNavigateProject).toHaveBeenCalledWith('proj-1');
    expect(onClose).toHaveBeenCalled();
  });

  it('runs an action result and closes', async () => {
    const user = userEvent.setup();
    const run = vi.fn();
    const onClose = vi.fn();
    render(
      <CommandPalette
        open
        captures={[]}
        actions={[{ id: 'a1', label: 'New capture', run }]}
        onNavigateCapture={() => {}}
        onNavigateProject={() => {}}
        onClose={onClose}
      />,
    );
    await user.click(screen.getByRole('button', { name: /New capture/ }));
    expect(run).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('excludes non-ready captures from results and never surfaces their doc title', async () => {
    const user = userEvent.setup();
    const captures = [
      makeCapture({
        id: 'cap-1',
        state: 'expired',
        doc: { title: 'SECRET TITLE', summary: '', steps: [], expected: null, actual: null, generator: 'deterministic', generatorModel: null },
      }),
    ];
    render(
      <CommandPalette
        open
        captures={captures}
        actions={[]}
        onNavigateCapture={() => {}}
        onNavigateProject={() => {}}
        onClose={() => {}}
      />,
    );
    expect(screen.queryByText('SECRET TITLE')).not.toBeInTheDocument();
    expect(screen.queryByText('cap-1')).not.toBeInTheDocument();

    await user.type(screen.getByLabelText('Search'), 'secret');
    expect(screen.queryByText('SECRET TITLE')).not.toBeInTheDocument();
  });

  it('deduplicates project ids and skips null projectIds', () => {
    const captures = [
      makeCapture({ id: 'cap-1', projectId: 'proj-1' }),
      makeCapture({ id: 'cap-2', projectId: 'proj-1' }),
      makeCapture({ id: 'cap-3', projectId: null }),
    ];
    render(
      <CommandPalette
        open
        captures={captures}
        actions={[]}
        onNavigateCapture={() => {}}
        onNavigateProject={() => {}}
        onClose={() => {}}
      />,
    );
    expect(screen.getAllByText('proj-1')).toHaveLength(1);
  });

  it('appears instantly (data-state=open, no closing) when opened', () => {
    render(<Wrapper initialOpen={true} />);
    expect(screen.getByRole('dialog').getAttribute('data-state')).toBe('open');
  });

  it('keeps the dialog mounted with data-state=closing during the 150ms fade, then unmounts', () => {
    vi.useFakeTimers();
    try {
      const { rerender, container } = render(
        <CommandPalette
          open
          captures={[]}
          actions={[]}
          onNavigateCapture={() => {}}
          onNavigateProject={() => {}}
          onClose={() => {}}
        />,
      );
      expect(screen.getByRole('dialog').getAttribute('data-state')).toBe('open');

      rerender(
        <CommandPalette
          open={false}
          captures={[]}
          actions={[]}
          onNavigateCapture={() => {}}
          onNavigateProject={() => {}}
          onClose={() => {}}
        />,
      );
      expect(screen.getByRole('dialog').getAttribute('data-state')).toBe('closing');

      act(() => {
        vi.advanceTimersByTime(149);
      });
      expect(screen.getByRole('dialog')).toBeInTheDocument();

      act(() => {
        vi.advanceTimersByTime(1);
      });
      expect(container).toBeEmptyDOMElement();
    } finally {
      vi.useRealTimers();
    }
  });

  it('cancels the pending close timer if reopened before the fade finishes', () => {
    vi.useFakeTimers();
    try {
      const { rerender } = render(
        <CommandPalette
          open
          captures={[]}
          actions={[]}
          onNavigateCapture={() => {}}
          onNavigateProject={() => {}}
          onClose={() => {}}
        />,
      );

      rerender(
        <CommandPalette
          open={false}
          captures={[]}
          actions={[]}
          onNavigateCapture={() => {}}
          onNavigateProject={() => {}}
          onClose={() => {}}
        />,
      );
      expect(screen.getByRole('dialog').getAttribute('data-state')).toBe('closing');

      act(() => {
        vi.advanceTimersByTime(50);
      });
      rerender(
        <CommandPalette
          open
          captures={[]}
          actions={[]}
          onNavigateCapture={() => {}}
          onNavigateProject={() => {}}
          onClose={() => {}}
        />,
      );
      expect(screen.getByRole('dialog').getAttribute('data-state')).toBe('open');

      act(() => {
        vi.advanceTimersByTime(150);
      });
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('unmounts on unmount while closing without throwing (cleanup clears the timer)', () => {
    vi.useFakeTimers();
    try {
      const { rerender, unmount } = render(
        <CommandPalette
          open
          captures={[]}
          actions={[]}
          onNavigateCapture={() => {}}
          onNavigateProject={() => {}}
          onClose={() => {}}
        />,
      );
      rerender(
        <CommandPalette
          open={false}
          captures={[]}
          actions={[]}
          onNavigateCapture={() => {}}
          onNavigateProject={() => {}}
          onClose={() => {}}
        />,
      );
      expect(() => {
        unmount();
      }).not.toThrow();
      expect(() => {
        act(() => {
          vi.advanceTimersByTime(200);
        });
      }).not.toThrow();
    } finally {
      vi.useRealTimers();
    }
  });
});
