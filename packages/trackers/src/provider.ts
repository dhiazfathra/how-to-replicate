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

/**
 * `id` is widened to include `'slack'` (task 14) — the only change this
 * task makes to the interface. `authorize`/`createIssue` are untouched:
 * Slack's "post a message" maps onto "create an issue" the same way
 * GitHub/GitLab's do (see slack.ts's `createIssue` for how a channel post
 * stands in for an issue: `id` becomes the message `ts`, `url` a permalink).
 */
export type TrackerProvider = {
  id: 'github' | 'gitlab' | 'slack';
  authorize(): Promise<AuthResult>;
  createIssue(input: IssueInput): Promise<CreatedIssue>;
};
