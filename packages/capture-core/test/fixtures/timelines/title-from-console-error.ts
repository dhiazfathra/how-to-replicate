import type { CaptureEvent } from '../../../src/types/event.js';
import type { TimelineFixture } from './types.js';

const navigation: CaptureEvent = {
  id: 'evt-1',
  captureId: 'cap-1',
  t: 0,
  kind: 'navigation',
  payload: { from: null, to: '/patients/123/edit', trigger: 'load' },
  redaction: { rulesApplied: [], fidelity: 'full' },
};

const warning: CaptureEvent = {
  id: 'evt-2',
  captureId: 'cap-1',
  t: 50,
  kind: 'console',
  payload: { level: 'warn', text: 'deprecated API used', stack: null },
  redaction: { rulesApplied: [], fidelity: 'full' },
};

const error: CaptureEvent = {
  id: 'evt-3',
  captureId: 'cap-1',
  t: 100,
  kind: 'console',
  payload: {
    level: 'error',
    text: 'TypeError: cannot read property of undefined',
    stack: 'at save (app.js:1:1)',
  },
  redaction: { rulesApplied: [], fidelity: 'full' },
};

export const titleFromConsoleError: TimelineFixture = {
  name: 'title prefers the first error-level console event over navigation',
  events: [navigation, warning, error],
  expected: {
    title: 'TypeError: cannot read property of undefined',
    summary: '1 step(s) recorded deterministically.',
    steps: [
      {
        n: 1,
        text: 'Navigated to /patients/123/edit',
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
