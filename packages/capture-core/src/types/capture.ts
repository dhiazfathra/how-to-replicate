import type { JsonValue, AssetRef, EnvSnapshot } from './asset.js';
import type { ReplicationDoc } from './doc.js';

export type CaptureState =
  | 'recording'
  | 'redacting'
  | 'composing'
  | 'ready'
  | 'failed'
  | 'expired';

export type Capture = {
  id: string;
  workspaceId: string | null;
  projectId: string | null;
  source: 'extension' | 'recording-link' | 'sdk' | 'cli' | 'ios';
  state: CaptureState;
  fidelity: 'full' | 'degraded';
  createdAt: string;
  epoch: number;
  env: EnvSnapshot;
  metadata: Record<string, JsonValue>;
  doc: ReplicationDoc | null;
  assets: AssetRef[];
  withheldEventCount: number;
  /** False once eviction has removed local events/assets. Absent means true (has local data). */
  localAssets?: boolean;
  sync: {
    revision: number;
    lastPushedAt: string | null;
    manifestComplete: boolean;
    dirtyFields: string[];
  };
};
