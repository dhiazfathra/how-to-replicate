import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Player } from './Player.js';

describe('Player', () => {
  it('renders an empty state with no src', () => {
    render(<Player src={null} scrubToMs={null} scrubToken={0} />);
    expect(screen.getByText('No video for this capture')).toBeInTheDocument();
  });

  it('renders a video element for a src', () => {
    render(<Player src="blob:fake" scrubToMs={null} scrubToken={0} />);
    expect(screen.getByTestId('player-video')).toHaveAttribute('src', 'blob:fake');
  });

  it('seeks the video to scrubToMs/1000 when scrubToken changes', () => {
    const { rerender } = render(<Player src="blob:fake" scrubToMs={2000} scrubToken={0} />);
    const video = screen.getByTestId('player-video') as HTMLVideoElement;
    expect(video.currentTime).toBe(2); // jsdom accepts currentTime assignment directly

    rerender(<Player src="blob:fake" scrubToMs={4000} scrubToken={1} />);
    expect(video.currentTime).toBe(4);
  });

  it('does nothing when scrubToMs is null', () => {
    const { rerender } = render(<Player src="blob:fake" scrubToMs={null} scrubToken={0} />);
    rerender(<Player src="blob:fake" scrubToMs={null} scrubToken={1} />);
    expect(screen.getByTestId('player-video')).toBeInTheDocument();
  });

  it('does not throw when scrubToMs changes with no src (no video element mounted)', () => {
    const { rerender } = render(<Player src={null} scrubToMs={1000} scrubToken={0} />);
    rerender(<Player src={null} scrubToMs={2000} scrubToken={1} />);
    expect(screen.getByText('No video for this capture')).toBeInTheDocument();
  });
});
