import { useEffect, useState } from 'react';
import type { Capture, CaptureEvent, Step } from '@htr/capture-core';
import { Timeline } from './components/Timeline.js';
import { Player } from './components/Player.js';
import { Steps } from './components/Steps.js';
import { FidelityBadge } from './components/FidelityBadge.js';
import type { ViewerRepo } from './App.js';

/**
 * Only ever mounted for a `capture.state === 'ready'` capture (App gates
 * that). Reads events/video bytes from the local repository — IndexedDB,
 * not the network — so this still honors local-first.
 */
export function CaptureDetail({ capture, repo }: { capture: Capture; repo: ViewerRepo }) {
  const [events, setEvents] = useState<CaptureEvent[]>([]);
  const [videoSrc, setVideoSrc] = useState<string | null>(null);
  const [kindFilter, setKindFilter] = useState<CaptureEvent['kind'] | 'all'>('all');
  const [selectedStep, setSelectedStep] = useState<Step | null>(null);
  const [scrubToken, setScrubToken] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void repo.readEvents(capture.id).then((loaded) => {
      if (!cancelled) setEvents(loaded);
    });
    return () => {
      cancelled = true;
    };
  }, [repo, capture.id]);

  useEffect(() => {
    const videoAsset = capture.assets.find((a) => a.kind === 'video');
    if (!videoAsset) {
      setVideoSrc(null);
      return;
    }
    let cancelled = false;
    let url: string | null = null;
    void repo.readAsset(videoAsset.id).then((bytes) => {
      if (cancelled || !bytes) return;
      url = URL.createObjectURL(new Blob([bytes.slice().buffer], { type: videoAsset.mimeType }));
      setVideoSrc(url);
    });
    return () => {
      cancelled = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [repo, capture.assets]);

  const steps = capture.doc?.steps ?? [];

  function selectStep(step: Step): void {
    setSelectedStep(step);
    if (step.tVideo !== null) setScrubToken((t) => t + 1);
  }

  return (
    <div className="capture-detail">
      <FidelityBadge fidelity={capture.fidelity} withheldEventCount={capture.withheldEventCount} />
      <h1>{capture.doc?.title ?? capture.id}</h1>
      <Player src={videoSrc} scrubToMs={selectedStep?.tVideo ?? null} scrubToken={scrubToken} />
      <Steps steps={steps} selectedStepN={selectedStep?.n ?? null} onSelect={selectStep} />
      <Timeline
        events={events}
        highlightedEventIds={selectedStep?.eventIds ?? []}
        kind={kindFilter}
        onKindChange={setKindFilter}
      />
    </div>
  );
}
