import type { CaptureEvent } from '../../../src/types/event.js';
import type { TimelineFixture } from './types.js';

function click(id: string, t: number): CaptureEvent {
  return {
    id,
    captureId: 'cap-1',
    t,
    kind: 'interaction',
    payload: {
      type: 'click',
      targetName: 'Retry',
      targetSelector: '#retry-btn',
      url: '/patients/123/edit',
      value: null,
    },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function scroll(id: string, t: number): CaptureEvent {
  return {
    id,
    captureId: 'cap-1',
    t,
    kind: 'interaction',
    payload: {
      type: 'scroll',
      targetName: 'window',
      targetSelector: 'window',
      url: '/patients/123/edit',
      value: null,
    },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

export const clickFoldAndScrollCoalesce: TimelineFixture = {
  name: 'repeated clicks fold into one step, scroll bursts coalesce into one step',
  events: [
    click('evt-1', 0),
    click('evt-2', 400),
    click('evt-3', 900),
    scroll('evt-4', 1200),
    scroll('evt-5', 1600),
  ],
  expected: {
    title: 'Untitled capture',
    summary: '2 step(s) recorded deterministically.',
    steps: [
      {
        n: 1,
        text: 'Clicked "Retry" on /patients/123/edit 3 times',
        eventIds: ['evt-1', 'evt-2', 'evt-3'],
        tVideo: null,
      },
      {
        n: 2,
        text: 'Scrolled on /patients/123/edit',
        eventIds: ['evt-4', 'evt-5'],
        tVideo: null,
      },
    ],
    expected: null,
    actual: null,
    generator: 'deterministic',
    generatorModel: null,
  },
};
