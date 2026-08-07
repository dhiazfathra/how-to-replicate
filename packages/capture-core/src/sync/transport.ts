import type { Mutation } from './mutations.js';

export type PushMutationsResult = {
  mutationId: string;
  applied: boolean;
  error: string;
};

export type PullDeltasResult = {
  mutations: Mutation[];
  revision: number;
  hasMore: boolean;
};

export type AssetUploadTicket = {
  uploadUrl: string;
  objectKey: string;
  requiredHeaders: Record<string, string>;
  expiresAtUnixMs: number;
};

export type CompleteAssetUploadResult = {
  verified: boolean;
  manifestComplete: boolean;
  error: string;
};

/**
 * The client-facing surface of sync-gateway's `SyncService`, shaped to mirror
 * proto/sync/v1/sync.proto closely enough to be a faithful contract, but kept
 * as a plain interface (not a generated ConnectRPC client) so tests inject a
 * fake and this package stays transport-agnostic. A real ConnectRPC-backed
 * implementation is a follow-up integration task once a TS proto target
 * exists — see task-7-report.md.
 */
export type SyncTransport = {
  pushMutations(mutations: Mutation[]): Promise<PushMutationsResult[]>;
  pullDeltas(workspaceId: string, since: number): Promise<PullDeltasResult>;
  requestAssetUpload(
    captureId: string,
    assetId: string,
    mimeType: string,
    sizeBytes: number,
    sha256: string,
  ): Promise<AssetUploadTicket>;
  completeAssetUpload(
    captureId: string,
    assetId: string,
    sha256: string,
  ): Promise<CompleteAssetUploadResult>;
};

/** The engine is inert (no push/pull ever runs) when no transport is configured. */
export function hasTransport(
  transport: SyncTransport | undefined,
): transport is SyncTransport {
  return transport != null;
}
