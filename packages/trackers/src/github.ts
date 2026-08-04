import type { AuthResult, CreatedIssue, IssueInput, TrackerProvider } from './provider.js';

const GITHUB_API = 'https://api.github.com';
const DEVICE_CODE_URL = 'https://github.com/login/device/code';
const ACCESS_TOKEN_URL = 'https://github.com/login/oauth/access_token';

export type DeviceCodeResponse = {
  device_code: string;
  user_code: string;
  verification_uri: string;
  expires_in: number;
  interval: number;
};

export type DevicePollResult =
  | { status: 'authorized'; auth: AuthResult }
  | { status: 'pending'; intervalSeconds: number }
  | { status: 'denied' }
  | { status: 'expired' };

type TokenErrorResponse = { error: string };
type TokenSuccessResponse = {
  access_token: string;
  refresh_token?: string;
  expires_in?: number;
};

export type GithubConfig = {
  clientId: string;
  scope: string;
  repo: string;
  /** Supplies the bearer token for `createIssue`; callers own token storage/refresh. */
  getAccessToken(): Promise<string>;
  fetch?: typeof fetch;
};

/**
 * No `client_secret` anywhere — the GitHub device flow (for public/native
 * OAuth apps) authenticates with `client_id` alone, which is precisely why
 * it, and not the web/confidential flow, is the one this package uses.
 */
export function createGithubProvider(config: GithubConfig): TrackerProvider & {
  requestDeviceCode(): Promise<DeviceCodeResponse>;
  pollDeviceCode(deviceCode: string, currentIntervalSeconds: number): Promise<DevicePollResult>;
};
export function createGithubProvider(config: GithubConfig) {
  const doFetch = config.fetch ?? fetch;

  async function requestDeviceCode(): Promise<DeviceCodeResponse> {
    const res = await doFetch(DEVICE_CODE_URL, {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/json' },
      body: JSON.stringify({ client_id: config.clientId, scope: config.scope }),
    });
    if (!res.ok) {
      throw new Error(`github: device code request failed with status ${res.status}`);
    }
    return (await res.json()) as DeviceCodeResponse;
  }

  /**
   * `currentIntervalSeconds` is the interval this poll was made at (the
   * caller's running state, seeded from the device-code response). Per
   * RFC 8628 §3.5, `slow_down` means "you are polling too fast **right
   * now**" — the new interval must be the current one plus at least 5
   * seconds, cumulatively, not a flat reset. `authorization_pending` means
   * no change: keep polling at the same interval the caller already has.
   */
  async function pollDeviceCode(
    deviceCode: string,
    currentIntervalSeconds: number,
  ): Promise<DevicePollResult> {
    const res = await doFetch(ACCESS_TOKEN_URL, {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/json' },
      body: JSON.stringify({
        client_id: config.clientId,
        device_code: deviceCode,
        grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
      }),
    });
    if (!res.ok) {
      throw new Error(`github: token poll failed with status ${res.status}`);
    }
    const json = (await res.json()) as TokenSuccessResponse | TokenErrorResponse;
    if ('error' in json) {
      switch (json.error) {
        case 'authorization_pending':
          return { status: 'pending', intervalSeconds: currentIntervalSeconds };
        case 'slow_down':
          return { status: 'pending', intervalSeconds: currentIntervalSeconds + 5 };
        case 'expired_token':
          return { status: 'expired' };
        case 'access_denied':
          return { status: 'denied' };
        default:
          throw new Error(`github: unrecognized device-flow error "${json.error}"`);
      }
    }
    return {
      status: 'authorized',
      auth: {
        accessToken: json.access_token,
        refreshToken: json.refresh_token ?? null,
        expiresInSeconds: json.expires_in ?? null,
      },
    };
  }

  /**
   * Runs the full device-flow poll loop: request a device code, then poll at
   * the server-advertised interval (honouring `slow_down` by backing off
   * cumulatively, per RFC 8628) until authorized, denied, or expired.
   */
  async function authorize(): Promise<AuthResult> {
    const { device_code, interval } = await requestDeviceCode();
    let waitSeconds = interval;
    for (;;) {
      const result = await pollDeviceCode(device_code, waitSeconds);
      if (result.status === 'authorized') return result.auth;
      if (result.status === 'denied') throw new Error('github: authorization denied');
      if (result.status === 'expired') throw new Error('github: device code expired');
      waitSeconds = result.intervalSeconds;
      await new Promise((resolve) => setTimeout(resolve, waitSeconds * 1000));
    }
  }

  async function createIssue(input: IssueInput): Promise<CreatedIssue> {
    const res = await doFetch(`${GITHUB_API}/repos/${config.repo}/issues`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        accept: 'application/vnd.github+json',
        authorization: `Bearer ${await config.getAccessToken()}`,
      },
      body: JSON.stringify({
        title: input.title,
        body: `${input.body}\n\n---\n[View capture](${input.captureUrl})`,
        labels: input.labels.map((l) => l.name),
      }),
    });
    if (!res.ok) {
      throw new Error(`github: create issue failed with status ${res.status}`);
    }
    const json = (await res.json()) as { html_url: string; id: number };
    return { url: json.html_url, id: String(json.id) };
  }

  return { id: 'github' as const, authorize, requestDeviceCode, pollDeviceCode, createIssue };
}

