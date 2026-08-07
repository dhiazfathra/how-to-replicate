import { describe, expect, it } from 'vitest';
import { generateDoc } from '../src/steps/generate.js';
import { timelineFixtures } from './fixtures/timelines/index.js';

describe('generateDoc (golden timeline fixtures)', () => {
  for (const fixture of timelineFixtures) {
    it(fixture.name, () => {
      const doc = generateDoc(
        fixture.events,
        fixture.assets ? { assets: fixture.assets } : {},
      );
      expect(doc).toEqual(fixture.expected);
    });
  }
});
