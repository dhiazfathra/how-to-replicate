import type { AuthResult, CreatedIssue, IssueInput, TrackerProvider } from './provider.js';

function base64UrlEncode(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** RFC 7636 `code_verifier`: 43-128 char unreserved-character string. */
export function generateCodeVerifier(): string {
  return base64UrlEncode(crypto.getRandomValues(new Uint8Array(64)));
}

/** RFC 7636 S256 `code_challenge` derived from `verifier`. */
export async function deriveCodeChallenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier));
  return base64UrlEncode(new Uint8Array(digest));
}

export type GitlabConfig = {
  clientId: string;
  redirectUri: string;
  host: string;
  projectId: string;
  getAccessToken(): Promise<string>;
  /** Supplies the `code` returned to `redirectUri` after the user completes the browser auth step. */
  getAuthorizationCode(authorizeUrl: string): Promise<string>;
  fetch?: typeof fetch;
};

type TokenResponse = { access_token: string; refresh_token?: string; expires_in?: number };

/**
 * PKCE (RFC 7636) — no `client_secret` anywhere. `verifier` is generated
 * locally and never leaves the client; only its SHA-256 digest
 * (`code_challenge`) is sent to `/oauth/authorize`, and the raw verifier is
 * sent once, at token-exchange time, so GitLab can confirm the same client
 * that started the flow is the one finishing it.
 */
export function createGitlabProvider(config: GitlabConfig): TrackerProvider {
  const doFetch = config.fetch ?? fetch;

  async function authorize(): Promise<AuthResult> {
    const verifier = generateCodeVerifier();
    const challenge = await deriveCodeChallenge(verifier);

    const authorizeUrl = new URL(`${config.host}/oauth/authorize`);
    authorizeUrl.searchParams.set('client_id', config.clientId);
    authorizeUrl.searchParams.set('redirect_uri', config.redirectUri);
    authorizeUrl.searchParams.set('response_type', 'code');
    authorizeUrl.searchParams.set('code_challenge', challenge);
    authorizeUrl.searchParams.set('code_challenge_method', 'S256');

    const code = await config.getAuthorizationCode(authorizeUrl.toString());

    const res = await doFetch(`${config.host}/oauth/token`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        client_id: config.clientId,
        code,
        grant_type: 'authorization_code',
        redirect_uri: config.redirectUri,
        code_verifier: verifier,
      }),
    });
    if (!res.ok) {
      throw new Error(`gitlab: token exchange failed with status ${res.status}`);
    }
    const json = (await res.json()) as TokenResponse;
    return {
      accessToken: json.access_token,
      refreshToken: json.refresh_token ?? null,
      expiresInSeconds: json.expires_in ?? null,
    };
  }

  async function createIssue(input: IssueInput): Promise<CreatedIssue> {
    const res = await doFetch(
      `${config.host}/api/v4/projects/${encodeURIComponent(config.projectId)}/issues`,
      {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${await config.getAccessToken()}`,
        },
        body: JSON.stringify({
          title: input.title,
          description: `${input.body}\n\n---\n[View capture](${input.captureUrl})`,
          labels: input.labels.map((l) => l.name).join(','),
        }),
      },
    );
    if (!res.ok) {
      throw new Error(`gitlab: create issue failed with status ${res.status}`);
    }
    const json = (await res.json()) as { web_url: string; id: number };
    return { url: json.web_url, id: String(json.id) };
  }

  return { id: 'gitlab' as const, authorize, createIssue };
}
