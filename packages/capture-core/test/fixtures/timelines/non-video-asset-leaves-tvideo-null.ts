import type { AssetRef } from '../../../src/types/asset.js';
import type { CaptureEvent } from '../../../src/types/event.js';
import type { TimelineFixture } from './types.js';

const click: CaptureEvent = {
  id: 'evt-1',
  captureId: 'cap-1',
  t: 250,
  kind: 'interaction',
  payload: {
    type: 'click',
    targetName: 'Save changes',
    targetSelector: '#save-btn',
    url: '/patients/123/edit',
    value: null,
  },
  redaction: { rulesApplied: [], fidelity: 'full' },
};

const screenshot: AssetRef = {
  id: 'asset-1',
  captureId: 'cap-1',
  kind: 'screenshot',
  mimeType: 'image/png',
  sizeBytes: 1024,
  chunkCount: 1,
  sha256: 'deadbeef',
};

export const nonVideoAssetLeavesTVideoNull: TimelineFixture = {
  name: 'a non-video asset does not set tVideo',
  events: [click],
  assets: [screenshot],
  expected: {
    title: 'Untitled capture',
    summary: '1 step(s) recorded deterministically.',
    steps: [
      {
        n: 1,
        text: 'Clicked "Save changes" on /patients/123/edit',
        eventIds: ['evt-1'],
        tVideo: null,
      },
    ],
    expected: null,
    actual: null,
    generator: 'deterministic',
    generatorModel: null,
  },
};
