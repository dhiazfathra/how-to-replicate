import { describe, expect, it } from 'vitest';
import { BLUR_PX, compositeFrame, validateRegions } from './blur.js';

// Dedicated test for this file's own re-export line — `recorder.test.ts`
// exercises `compositeFrame`/`validateRegions` transitively through
// `recorder.ts`, but that's not a substitute for direct coverage of this
// module: transitive coverage depends on which other test files a given
// coverage run happens to schedule/merge, which is exactly what made this
// file's coverage flaky. See `packages/capture-core/src/media/blur.test.ts`
// for the exhaustive behavioral tests of the underlying implementation.
describe('offscreen blur re-export', () => {
  it('re-exports the capture-core compositing primitives unchanged', () => {
    expect(BLUR_PX).toBe(24);
    expect(typeof compositeFrame).toBe('function');
    expect(typeof validateRegions).toBe('function');
  });
});
