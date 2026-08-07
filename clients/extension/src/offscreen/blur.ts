/**
 * Task 12's compositing primitive moved to `@htr/capture-core` in Task 14
 * (it had no `chrome.*`/extension-specific dependency, so
 * `clients/recording-link` can share it too). Re-exported here so existing
 * import paths in this client (`./blur.js`) keep working without drift
 * between two copies of a compliance-critical routine.
 */
export { BLUR_PX, compositeFrame, validateRegions, type DrawTarget, type RectLike } from '@htr/capture-core';
