import { useEffect, useRef } from 'react';

/**
 * `scrubToMs` is a ms-offset from `capture.epoch` (never wall-clock — see
 * repo conventions). Every change seeks the underlying `<video>`, including
 * a re-click of the same step, so `scrubToken` disambiguates identical
 * `scrubToMs` values fired twice in a row.
 */
export function Player({
  src,
  scrubToMs,
  scrubToken,
}: {
  src: string | null;
  scrubToMs: number | null;
  scrubToken: number;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);

  useEffect(() => {
    if (scrubToMs === null) return;
    const video = videoRef.current;
    if (!video) return;
    video.currentTime = scrubToMs / 1000;
    // scrubToken (not scrubToMs) is the intended dependency: re-clicking the
    // same step must re-seek even when the ms value is unchanged.
  }, [scrubToken]);

  if (!src) {
    return <div className="player player--empty">No video for this capture</div>;
  }

  return <video ref={videoRef} className="player" src={src} controls data-testid="player-video" />;
}
