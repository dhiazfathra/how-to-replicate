import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import type { Step } from '@htr/capture-core';
import { Steps } from './Steps.js';

const steps: Step[] = [
  { n: 1, text: 'Click login', eventIds: ['e1'], tVideo: 1000 },
  { n: 2, text: 'Observe error with no frame', eventIds: ['e2'], tVideo: null },
];

describe('Steps', () => {
  it('renders every step, including one with no tVideo', () => {
    render(<Steps steps={steps} selectedStepN={null} onSelect={() => {}} />);
    expect(screen.getByText('Click login')).toBeInTheDocument();
    expect(screen.getByText('Observe error with no frame')).toBeInTheDocument();
    expect(screen.getByText('no video')).toBeInTheDocument();
  });

  it('marks the selected step as current', () => {
    render(<Steps steps={steps} selectedStepN={2} onSelect={() => {}} />);
    expect(screen.getByRole('button', { name: /Observe error/ })).toHaveAttribute('aria-current', 'true');
    expect(screen.getByRole('button', { name: /Click login/ })).toHaveAttribute('aria-current', 'false');
  });

  it('calls onSelect with the clicked step', () => {
    const onSelect = vi.fn();
    render(<Steps steps={steps} selectedStepN={null} onSelect={onSelect} />);
    screen.getByRole('button', { name: /Click login/ }).click();
    expect(onSelect).toHaveBeenCalledWith(steps[0]);
  });
});
