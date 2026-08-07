import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { FidelityBadge } from './FidelityBadge.js';

describe('FidelityBadge', () => {
  it('renders nothing for a full-fidelity capture with no withheld events', () => {
    const { container } = render(<FidelityBadge fidelity="full" withheldEventCount={0} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders the degraded label prominently', () => {
    render(<FidelityBadge fidelity="degraded" withheldEventCount={0} />);
    expect(screen.getByText('Degraded capture')).toBeInTheDocument();
  });

  it('renders the withheld event count message', () => {
    render(<FidelityBadge fidelity="full" withheldEventCount={3} />);
    expect(screen.getByText('3 events withheld by redaction policy')).toBeInTheDocument();
  });

  it('renders both signals together', () => {
    render(<FidelityBadge fidelity="degraded" withheldEventCount={5} />);
    expect(screen.getByText('Degraded capture')).toBeInTheDocument();
    expect(screen.getByText('5 events withheld by redaction policy')).toBeInTheDocument();
  });
});
