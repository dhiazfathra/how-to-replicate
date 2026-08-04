import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import type { CaptureEvent } from '@htr/capture-core';
import { Timeline } from './Timeline.js';

function event(overrides: Partial<CaptureEvent>): CaptureEvent {
  return {
    id: 'e1',
    captureId: 'c1',
    t: 0,
    kind: 'console',
    payload: { level: 'log', text: 'hi', stack: null },
    redaction: { rulesApplied: [], fidelity: 'full' },
    ...overrides,
  };
}

describe('Timeline', () => {
  it('renders all events when kind is "all"', () => {
    const events = [event({ id: 'e1', kind: 'console' }), event({ id: 'e2', kind: 'network' })];
    render(<Timeline events={events} kind="all" onKindChange={() => {}} />);
    expect(screen.getAllByRole('listitem')).toHaveLength(2);
  });

  it('filters events by kind', () => {
    const events = [event({ id: 'e1', kind: 'console' }), event({ id: 'e2', kind: 'network' })];
    render(<Timeline events={events} kind="network" onKindChange={() => {}} />);
    const items = screen.getAllByRole('listitem');
    expect(items).toHaveLength(1);
    expect(items[0]).toHaveAttribute('data-event-id', 'e2');
  });

  it('highlights the cited events', () => {
    const events = [event({ id: 'e1' }), event({ id: 'e2' })];
    render(<Timeline events={events} kind="all" highlightedEventIds={['e2']} onKindChange={() => {}} />);
    const highlighted = screen.getByText('console', { selector: '.timeline__event--highlighted .timeline__event-kind' });
    expect(highlighted).toBeInTheDocument();
  });

  it('calls onKindChange when a filter button is clicked', () => {
    const onKindChange = vi.fn();
    const events = [event({})];
    render(<Timeline events={events} kind="all" onKindChange={onKindChange} />);
    screen.getByRole('button', { name: 'network' }).click();
    expect(onKindChange).toHaveBeenCalledWith('network');
  });

  it('calls onKindChange when the "all" filter is clicked', () => {
    const onKindChange = vi.fn();
    render(<Timeline events={[event({})]} kind="console" onKindChange={onKindChange} />);
    screen.getByRole('button', { name: 'all' }).click();
    expect(onKindChange).toHaveBeenCalledWith('all');
  });

  it('marks the active filter as pressed', () => {
    render(<Timeline events={[]} kind="console" onKindChange={() => {}} />);
    expect(screen.getByRole('button', { name: 'console' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'all' })).toHaveAttribute('aria-pressed', 'false');
  });
});
