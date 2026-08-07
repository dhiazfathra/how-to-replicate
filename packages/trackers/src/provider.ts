import type { AssetRef } from '@htr/capture-core';

/** Result of a completed OAuth handshake — see github.ts / gitlab.ts for how each is obtained. */
export type AuthResult = {
  accessToken: string;
  refreshToken: string | null;
  expiresInSeconds: number | null;
};

export type IssueLabel = {
  name: string;
  source: 'project' | 'error-signature';
};

/**
 * Everything needed to file an issue. `body` is the rendered replication
 * document (markdown), not the raw `ReplicationDoc` — rendering is the
 * caller's job (see `@htr/capture-core`'s `export/markdown.ts`) so this
 * package stays tracker-shaped, not doc-shaped.
 */
export type IssueInput = {
  title: string;
  body: string;
  assets: AssetRef[];
  captureUrl: string;
  labels: IssueLabel[];
};

export type CreatedIssue = {
  url: string;
  id: string;
};

export type TrackerProvider = {
  id: 'github' | 'gitlab';
  authorize(): Promise<AuthResult>;
  createIssue(input: IssueInput): Promise<CreatedIssue>;
};
