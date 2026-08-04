import { describe, expect, it, vi } from 'vitest';
import { createGithubProvider } from './github.js';
import deviceCode from './fixtures/github-device-code.json' with { type: 'json' };
import pollPending from './fixtures/github-poll-pending.json' with { type: 'json' };
import pollSlowDown from './fixtures/github-poll-slow-down.json' with { type: 'json' };
import pollExpired from './fixtures/github-poll-expired.json' with { type: 'json' };
import pollDenied from './fixtures/github-poll-denied.json' with { type: 'json' };
import pollSuccess from './fixtures/github-poll-success.json' with { type: 'json' };
import issueCreated from './fixtures/github-issue-created.json' with { type: 'json' };

function jsonResponse(body: unknown, ok = true, status = 200): Response {
  return { ok, status, json: () => Promise.resolve(body) } as Response;
}

const baseConfig = {
  clientId: 'client-id',
  scope: 'repo',
  repo: 'octo-org/octo-repo',
  getAccessToken: () => Promise.resolve('token'),
};

describe('createGithubProvider requestDeviceCode / pollDeviceCode', () => {
  it('requests a device code with no client_secret in the body', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(deviceCode));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    const result = await provider.requestDeviceCode();

    expect(result).toEqual(deviceCode);
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('https://github.com/login/device/code');
    const body = JSON.parse(init.body as string) as Record<string, unknown>;
    expect(body).toEqual({ client_id: 'client-id', scope: 'repo' });
    expect(body).not.toHaveProperty('client_secret');
  });

  it('throws when the device code request fails', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({}, false, 500));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    await expect(provider.requestDeviceCode()).rejects.toThrow(/status 500/);
  });

  it('surfaces authorization_pending as pending', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(pollPending));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    const result = await provider.pollDeviceCode('device-code');

    expect(result).toEqual({ status: 'pending', intervalSeconds: 5 });
  });

  it('surfaces slow_down as pending with a longer interval', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(pollSlowDown));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    const result = await provider.pollDeviceCode('device-code');

    expect(result).toEqual({ status: 'pending', intervalSeconds: 10 });
  });

  it('surfaces expired_token as terminal expired', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(pollExpired));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    expect(await provider.pollDeviceCode('device-code')).toEqual({ status: 'expired' });
  });

  it('surfaces access_denied as terminal denied', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(pollDenied));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    expect(await provider.pollDeviceCode('device-code')).toEqual({ status: 'denied' });
  });

  it('throws on an unrecognized error code', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ error: 'something_else' }));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    await expect(provider.pollDeviceCode('device-code')).rejects.toThrow(/something_else/);
  });

  it('throws when the poll request fails', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({}, false, 500));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    await expect(provider.pollDeviceCode('device-code')).rejects.toThrow(/status 500/);
  });

  it('returns authorized with the access/refresh tokens on success', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(pollSuccess));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    const result = await provider.pollDeviceCode('device-code');

    expect(result).toEqual({
      status: 'authorized',
      auth: { accessToken: pollSuccess.access_token, refreshToken: null, expiresInSeconds: null },
    });
    const [, init] = fetch.mock.calls[0] as [string, RequestInit];
    const sentBody = JSON.parse(init.body as string) as Record<string, unknown>;
    expect(sentBody.grant_type).toBe('urn:ietf:params:oauth:grant-type:device_code');
    expect(sentBody).not.toHaveProperty('client_secret');
  });
});

describe('createGithubProvider authorize', () => {
  it('runs the full poll loop honouring slow_down before authorizing', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(deviceCode))
      .mockResolvedValueOnce(jsonResponse(pollPending))
      .mockResolvedValueOnce(jsonResponse(pollSlowDown))
      .mockResolvedValueOnce(jsonResponse(pollSuccess));
    vi.useFakeTimers();
    try {
      const provider = createGithubProvider({ ...baseConfig, fetch });
      const authPromise = provider.authorize();
      await vi.runAllTimersAsync();
      const result = await authPromise;
      expect(result.accessToken).toBe(pollSuccess.access_token);
      expect(fetch).toHaveBeenCalledTimes(4);
    } finally {
      vi.useRealTimers();
    }
  });

  it('throws on denied', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(deviceCode))
      .mockResolvedValueOnce(jsonResponse(pollDenied));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    await expect(provider.authorize()).rejects.toThrow(/denied/);
  });

  it('throws on expired', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(deviceCode))
      .mockResolvedValueOnce(jsonResponse(pollExpired));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    await expect(provider.authorize()).rejects.toThrow(/expired/);
  });
});

describe('createGithubProvider createIssue', () => {
  it('posts the issue and includes the capture deep link and labels', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(issueCreated));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    const result = await provider.createIssue({
      title: 'Repro: crash on submit',
      body: '1. Click submit',
      assets: [],
      captureUrl: 'https://app.example.com/capture/01H',
      labels: [{ name: 'project:demo', source: 'project' }],
    });

    expect(result).toEqual({ url: issueCreated.html_url, id: String(issueCreated.id) });
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('https://api.github.com/repos/octo-org/octo-repo/issues');
    expect((init.headers as Record<string, string>).authorization).toBe('Bearer token');
    const body = JSON.parse(init.body as string) as { body: string; labels: string[] };
    expect(body.body).toContain('https://app.example.com/capture/01H');
    expect(body.labels).toEqual(['project:demo']);
  });

  it('throws when issue creation fails', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({}, false, 422));
    const provider = createGithubProvider({ ...baseConfig, fetch });

    await expect(
      provider.createIssue({
        title: 't',
        body: 'b',
        assets: [],
        captureUrl: 'https://x',
        labels: [],
      }),
    ).rejects.toThrow(/status 422/);
  });

  it('id is github', () => {
    const provider = createGithubProvider({ ...baseConfig, fetch: vi.fn() });
    expect(provider.id).toBe('github');
  });

  it('falls back to the global fetch when none is injected', async () => {
    const globalFetch = vi.fn().mockResolvedValue(jsonResponse(issueCreated));
    vi.stubGlobal('fetch', globalFetch);
    try {
      const provider = createGithubProvider(baseConfig);
      await provider.createIssue({ title: 't', body: 'b', assets: [], captureUrl: 'https://x', labels: [] });
      expect(globalFetch).toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
