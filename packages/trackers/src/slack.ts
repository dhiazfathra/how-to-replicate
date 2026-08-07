import type { AssetRef } from '@htr/capture-core';
import { generateCodeVerifier, deriveCodeChallenge } from './gitlab.js';
import type { AuthResult, CreatedIssue, IssueInput, TrackerProvider } from './provider.js';

const AUTHORIZE_URL = 'https://slack.com/oauth/v2/authorize';
const TOKEN_URL = 'https://slack.com/api/oauth.v2.access';
const POST_MESSAGE_URL = 'https://slack.com/api/chat.postMessage';

/**
 * Above this, a video is too large to feel like a native "attachment" in a
 * Slack message and is posted as an explicit download link instead.
 * ponytail: arbitrary threshold, tune against real Slack unfurl behaviour
 * once this ships against a live workspace.
 */
const INLINE_VIDEO_MAX_BYTES = 50 * 1024 * 1024;

export type SlackConfig = {
  clientId: string;
  redirectUri: string;
  /** Resolved per-project target — see `resolveSlackBinding`. */
  channelId: string;
  getAccessToken(): Promise<string>;
  /** Supplies the `code` returned to `redirectUri` after the user completes the browser auth step. */
  getAuthorizationCode(authorizeUrl: string): Promise<string>;
  /** Resolves a fetchable/signed URL for an asset; this package never sees raw asset bytes. */
  getAssetUrl(asset: AssetRef): Promise<string>;
  fetch?: typeof fetch;
};

type SlackErrorResponse = { ok: false; error: string };
type SlackTokenResponse = {
  ok: true;
  access_token: string;
  refresh_token?: string;
  expires_in?: number;
};
type SlackPostMessageResponse = { ok: true; channel: string; ts: string };

/**
 * A binding row's client-side shape (the server table is `integration_bindings`
 * — id/workspace_id/provider/config jsonb, task 3's migration). There is no
 * existing client-side binding-resolution helper in this package to mirror
 * (github.ts/gitlab.ts take an already-resolved `repo`/`projectId` in their
 * config, resolved by whatever constructs them) — this is the same shape,
 * just made resolvable from a list of rows instead of hardcoded at
 * construction time.
 */
export type SlackBinding = {
  id: string;
  workspaceId: string;
  provider: 'slack';
  config: { projectId: string; channelId: string };
};

/** Picks the binding whose `config.projectId` matches, per-project as invariant demands. */
export function resolveSlackBinding(bindings: SlackBinding[], projectId: string): SlackBinding {
  const binding = bindings.find((b) => b.provider === 'slack' && b.config.projectId === projectId);
  if (!binding) {
    throw new Error(`slack: no binding for project "${projectId}"`);
  }
  return binding;
}

type ParsedBody = {
  summary: string;
  steps: string[];
  expected: string | null;
  actual: string | null;
};

/**
 * `IssueInput.body` is the flattened text `router.ts` builds identically for
 * every tracker (see its docstring: rendering is upstream, providers stay
 * tracker-shaped). Slack block-kit wants structure, not a wall of markdown,
 * so this re-splits that deterministic format back into parts — the
 * alternative would be changing `IssueInput` to carry structured fields,
 * which is exactly the interface change this task is told not to make.
 */
function parseBody(body: string): ParsedBody {
  const lines = body.split('\n');
  const steps: string[] = [];
  const summaryLines: string[] = [];
  let expected: string | null = null;
  let actual: string | null = null;

  for (const line of lines) {
    const stepMatch = /^\d+\.\s(.*)$/.exec(line);
    const expectedMatch = /^Expected: (.*)$/.exec(line);
    const actualMatch = /^Actual: (.*)$/.exec(line);
    // The capture group always matches (possibly empty) once the regex matches at all.
    if (stepMatch) {
      steps.push(stepMatch[1] as string);
    } else if (expectedMatch) {
      expected = expectedMatch[1] as string;
    } else if (actualMatch) {
      actual = actualMatch[1] as string;
    } else if (steps.length === 0 && line !== '') {
      summaryLines.push(line);
    }
  }

  return { summary: summaryLines.join('\n'), steps, expected, actual };
}

async function buildBlocks(
  input: IssueInput,
  config: SlackConfig,
): Promise<Array<Record<string, unknown>>> {
  const { summary, steps, expected, actual } = parseBody(input.body);
  const blocks: Array<Record<string, unknown>> = [
    { type: 'header', text: { type: 'plain_text', text: input.title.slice(0, 150) } },
  ];

  if (summary) {
    blocks.push({ type: 'section', text: { type: 'mrkdwn', text: summary } });
  }

  if (steps.length > 0) {
    const text = steps.map((s, i) => `${i + 1}. ${s}`).join('\n');
    blocks.push({ type: 'section', text: { type: 'mrkdwn', text } });
  }

  if (expected || actual) {
    const fields: Array<{ type: 'mrkdwn'; text: string }> = [];
    if (expected) fields.push({ type: 'mrkdwn', text: `*Expected*\n${expected}` });
    if (actual) fields.push({ type: 'mrkdwn', text: `*Actual*\n${actual}` });
    blocks.push({ type: 'section', fields });
  }

  if (input.labels.length > 0) {
    const text = input.labels.map((l) => `\`${l.name}\``).join(' ');
    blocks.push({ type: 'context', elements: [{ type: 'mrkdwn', text }] });
  }

  blocks.push({
    type: 'section',
    text: { type: 'mrkdwn', text: `<${input.captureUrl}|View capture>` },
  });

  const video = input.assets.find((a) => a.kind === 'video');
  if (video) {
    const url = await config.getAssetUrl(video);
    if (video.sizeBytes <= INLINE_VIDEO_MAX_BYTES) {
      blocks.push({ type: 'section', text: { type: 'mrkdwn', text: `<${url}|Video>` } });
    } else {
      blocks.push({
        type: 'actions',
        elements: [
          {
            type: 'button',
            text: { type: 'plain_text', text: 'Download video (signed link)' },
            url,
          },
        ],
      });
    }
  }

  return blocks;
}

/**
 * PKCE (RFC 7636) — no `client_secret` anywhere, mirroring gitlab.ts's
 * approach exactly (same helper functions, reused rather than duplicated).
 * Slack's OAuth v2 endpoints support PKCE the same way: `code_challenge`
 * goes to `/oauth/v2/authorize`, the raw `code_verifier` goes once to the
 * token exchange.
 */
export function createSlackProvider(config: SlackConfig): TrackerProvider {
  const doFetch = config.fetch ?? fetch;

  async function authorize(): Promise<AuthResult> {
    const verifier = generateCodeVerifier();
    const challenge = await deriveCodeChallenge(verifier);

    const authorizeUrl = new URL(AUTHORIZE_URL);
    authorizeUrl.searchParams.set('client_id', config.clientId);
    authorizeUrl.searchParams.set('redirect_uri', config.redirectUri);
    authorizeUrl.searchParams.set('code_challenge', challenge);
    authorizeUrl.searchParams.set('code_challenge_method', 'S256');

    const code = await config.getAuthorizationCode(authorizeUrl.toString());

    const res = await doFetch(TOKEN_URL, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        client_id: config.clientId,
        code,
        redirect_uri: config.redirectUri,
        code_verifier: verifier,
      }),
    });
    if (!res.ok) {
      throw new Error(`slack: token exchange failed with status ${res.status}`);
    }
    const json = (await res.json()) as SlackTokenResponse | SlackErrorResponse;
    if (!json.ok) {
      throw new Error(`slack: token exchange failed with error "${json.error}"`);
    }
    return {
      accessToken: json.access_token,
      refreshToken: json.refresh_token ?? null,
      expiresInSeconds: json.expires_in ?? null,
    };
  }

  async function createIssue(input: IssueInput): Promise<CreatedIssue> {
    const blocks = await buildBlocks(input, config);
    const res = await doFetch(POST_MESSAGE_URL, {
      method: 'POST',
      headers: {
        'content-type': 'application/json; charset=utf-8',
        authorization: `Bearer ${await config.getAccessToken()}`,
      },
      body: JSON.stringify({ channel: config.channelId, text: input.title, blocks }),
    });
    if (!res.ok) {
      throw new Error(`slack: post message failed with status ${res.status}`);
    }
    const json = (await res.json()) as SlackPostMessageResponse | SlackErrorResponse;
    if (!json.ok) {
      throw new Error(`slack: post message failed with error "${json.error}"`);
    }
    const permalinkTs = json.ts.replace('.', '');
    return { url: `https://slack.com/archives/${json.channel}/p${permalinkTs}`, id: json.ts };
  }

  return { id: 'slack' as const, authorize, createIssue };
}
