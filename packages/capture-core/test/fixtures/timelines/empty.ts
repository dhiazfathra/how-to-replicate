import type { TimelineFixture } from './types.js';

export const empty: TimelineFixture = {
  name: 'empty timeline produces a step-free, generic doc',
  events: [],
  expected: {
    title: 'Untitled capture',
    summary: '0 step(s) recorded deterministically.',
    steps: [],
    expected: null,
    actual: null,
    generator: 'deterministic',
    generatorModel: null,
  },
};
