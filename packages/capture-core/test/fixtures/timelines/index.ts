import { allInteractionTypes } from './all-interaction-types.js';
import { clickFoldAndScrollCoalesce } from './click-fold-and-scroll-coalesce.js';
import { empty } from './empty.js';
import { exactClickFormat } from './exact-click-format.js';
import { nonVideoAssetLeavesTVideoNull } from './non-video-asset-leaves-tvideo-null.js';
import { titleFromConsoleError } from './title-from-console-error.js';
import { titleFromLastNavigation } from './title-from-last-navigation.js';
import type { TimelineFixture } from './types.js';
import { videoAssetSetsTVideo } from './video-asset-sets-tvideo.js';

export const timelineFixtures: TimelineFixture[] = [
  empty,
  exactClickFormat,
  clickFoldAndScrollCoalesce,
  allInteractionTypes,
  titleFromConsoleError,
  titleFromLastNavigation,
  videoAssetSetsTVideo,
  nonVideoAssetLeavesTVideoNull,
];
