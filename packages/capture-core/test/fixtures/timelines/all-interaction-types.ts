import type { CaptureEvent } from '../../../src/types/event.js';
import type { TimelineFixture } from './types.js';

function interaction(
  id: string,
  t: number,
  payload: CaptureEvent['payload'],
): CaptureEvent {
  return {
    id,
    captureId: 'cap-1',
    t,
    kind: 'interaction',
    payload,
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

export const allInteractionTypes: TimelineFixture = {
  name: 'input, keydown and submit each render their own mechanical prose',
  events: [
    interaction('evt-1', 0, {
      type: 'input',
      targetName: 'Email',
      targetSelector: '#email',
      url: '/login',
      value: 'a@example.com',
    }),
    interaction('evt-2', 100, {
      type: 'keydown',
      targetName: 'Email',
      targetSelector: '#email',
      url: '/login',
      value: 'Enter',
    }),
    interaction('evt-3', 200, {
      type: 'submit',
      targetName: 'Login form',
      targetSelector: '#login-form',
      url: '/login',
      value: null,
    }),
    interaction('evt-4', 300, {
      type: 'keydown',
      targetName: 'Email',
      targetSelector: '#email',
      url: '/login',
      value: null,
    }),
  ],
  expected: {
    title: 'Untitled capture',
    summary: '4 step(s) recorded deterministically.',
    steps: [
      {
        n: 1,
        text: 'Typed into "Email" on /login',
        eventIds: ['evt-1'],
        tVideo: null,
      },
      {
        n: 2,
        text: 'Pressed "Enter" on /login',
        eventIds: ['evt-2'],
        tVideo: null,
      },
      {
        n: 3,
        text: 'Submitted "Login form" on /login',
        eventIds: ['evt-3'],
        tVideo: null,
      },
      {
        n: 4,
        text: 'Pressed "" on /login',
        eventIds: ['evt-4'],
        tVideo: null,
      },
    ],
    expected: null,
    actual: null,
    generator: 'deterministic',
    generatorModel: null,
  },
};
