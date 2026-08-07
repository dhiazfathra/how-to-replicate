import { describe, expect, it } from 'vitest';
import { deriveLabels } from './labels.js';

describe('deriveLabels', () => {
  it('returns a project label when projectId is set', () => {
    expect(deriveLabels('demo', '')).toEqual([{ name: 'project:demo', source: 'project' }]);
  });

  it('omits the project label when projectId is null', () => {
    expect(deriveLabels(null, '')).toEqual([]);
  });

  it.each([
    ["Cannot read property 'x' of undefined", 'error:null-pointer'],
    ['Request timed out after 30s', 'error:timeout'],
    ['fetch failed: ECONNREFUSED', 'error:network'],
    ['403 Forbidden', 'error:permission'],
    ['panic: runtime error', 'error:crash'],
  ])('detects %s -> %s', (text, expected) => {
    const labels = deriveLabels(null, text);
    expect(labels.map((l) => l.name)).toContain(expected);
  });

  it('detects multiple signatures and a project label together', () => {
    const labels = deriveLabels('demo', 'network error then unauthorized');
    expect(labels).toEqual([
      { name: 'project:demo', source: 'project' },
      { name: 'error:network', source: 'error-signature' },
      { name: 'error:permission', source: 'error-signature' },
    ]);
  });

  it('returns no error-signature labels for unrelated text', () => {
    expect(deriveLabels(null, 'everything worked fine')).toEqual([]);
  });
});
