import type { CaptureEvent } from '../../../src/types/event.js';
import type { TimelineFixture } from './types.js';

function nav(id: string, t: number, to: string): CaptureEvent {
  return {
    id,
    captureId: 'cap-1',
    t,
    kind: 'navigation',
    payload: { from: null, to, trigger: 'pushstate' },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

export const titleFromLastNavigation: TimelineFixture = {
  name: 'title falls back to the last navigation when there is no console error',
  events: [nav('evt-1', 0, '/patients'), nav('evt-2', 100, '/patients/123/edit')],
  expected: {
    title: 'Navigate to /patients/123/edit',
    summary: '2 step(s) recorded deterministically.',
    steps: [
      { n: 1, text: 'Navigated to /patients', eventIds: ['evt-1'], tVideo: null },
      {
        n: 2,
        text: 'Navigated to /patients/123/edit',
        eventIds: ['evt-2'],
        tVideo: null,
      },
    ],
    expected: null,
    actual: null,
    generator: 'deterministic',
    generatorModel: null,
  },
};
