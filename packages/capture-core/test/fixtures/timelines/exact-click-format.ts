import type { CaptureEvent } from '../../../src/types/event.js';
import type { TimelineFixture } from './types.js';

const click: CaptureEvent = {
  id: 'evt-1',
  captureId: 'cap-1',
  t: 1000,
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

export const exactClickFormat: TimelineFixture = {
  name: 'a single click renders the spec-mandated exact output shape',
  events: [click],
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
