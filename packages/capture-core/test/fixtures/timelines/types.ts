import type { AssetRef } from '../../../src/types/asset.js';
import type { ReplicationDoc } from '../../../src/types/doc.js';
import type { CaptureEvent } from '../../../src/types/event.js';

export type TimelineFixture = {
  name: string;
  events: CaptureEvent[];
  assets?: AssetRef[];
  expected: ReplicationDoc;
};
