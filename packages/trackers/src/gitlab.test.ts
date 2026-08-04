import { describe, expect, it, vi } from 'vitest';
import { createGitlabProvider, generateCodeVerifier, deriveCodeChallenge } from './gitlab.js';
import gitlabToken from './fixtures/gitlab-token.json' with { type: 'json' };
import issueCreated from './fixtures/gitlab-issue-created.json' with { type: 'json' };

function jsonResponse(body: unknown, ok = true, status = 200): Response {
  return { ok, status, json: () => Promise.resolve(body) } as Response;
}

const baseConfig = {
  clientId: 'client-id',
  redirectUri: 'https://app.example.com/oauth/callback',
  host: 'https://gitlab.example.com',
  projectId: 'htr/demo',
  getAccessToken: () => Promise.resolve('token'),
};

describe('PKCE verifier/challenge derivation', () => {
  it('generates a verifier in the RFC 7636 unreserved character set', () => {
    const verifier = generateCodeVerifier();
    expect(verifier.length).toBeGreaterThanOrEqual(43);
    expect(verifier).toMatch(/^[A-Za-z0-9\-._~]+$/);
  });

  it('derives an S256 challenge deterministically from a verifier', async () => {
    // RFC 7636 appendix B worked example.
    const verifier = 'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk';
    const challenge = await deriveCodeChallenge(verifier);
    expect(challenge).toBe('E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM');
  });

  it('produces a different challenge for a different verifier', async () => {
    const a = await deriveCodeChallenge(generateCodeVerifier());
    const b = await deriveCodeChallenge(generateCodeVerifier());
    expect(a).not.toBe(b);
  });
});

describe('createGitlabProvider authorize', () => {
  it('exchanges the code for tokens using code_verifier, no client_secret', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(gitlabToken));
    let capturedAuthorizeUrl = '';
    const provider = createGitlabProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: (authorizeUrl) => {
        capturedAuthorizeUrl = authorizeUrl;
        return Promise.resolve('returned-auth-code');
      },
    });

    const result = await provider.authorize();

    expect(capturedAuthorizeUrl).toContain('code_challenge_method=S256');
    expect(capturedAuthorizeUrl).toContain('response_type=code');
    expect(result).toEqual({
      accessToken: gitlabToken.access_token,
      refreshToken: gitlabToken.refresh_token,
      expiresInSeconds: gitlabToken.expires_in,
    });
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('https://gitlab.example.com/oauth/token');
    const body = JSON.parse(init.body as string) as Record<string, unknown>;
    expect(body.code).toBe('returned-auth-code');
    expect(body).toHaveProperty('code_verifier');
    expect(body).not.toHaveProperty('client_secret');
  });

  it('throws when the token exchange fails', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({}, false, 400));
    const provider = createGitlabProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await expect(provider.authorize()).rejects.toThrow(/status 400/);
  });

  it('handles a token response with no refresh_token/expires_in', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ access_token: 'only-access' }));
    const provider = createGitlabProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    const result = await provider.authorize();

    expect(result).toEqual({ accessToken: 'only-access', refreshToken: null, expiresInSeconds: null });
  });
});

describe('createGitlabProvider createIssue', () => {
  it('posts the issue with a deep link and labels', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(issueCreated));
    const provider = createGitlabProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    const result = await provider.createIssue({
      title: 'Repro: crash on submit',
      body: '1. Click submit',
      assets: [],
      captureUrl: 'https://app.example.com/capture/01H',
      labels: [{ name: 'project:demo', source: 'project' }],
    });

    expect(result).toEqual({ url: issueCreated.web_url, id: String(issueCreated.id) });
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('https://gitlab.example.com/api/v4/projects/htr%2Fdemo/issues');
    expect((init.headers as Record<string, string>).authorization).toBe('Bearer token');
    const body = JSON.parse(init.body as string) as { description: string; labels: string };
    expect(body.description).toContain('https://app.example.com/capture/01H');
    expect(body.labels).toBe('project:demo');
  });

  it('throws when issue creation fails', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({}, false, 500));
    const provider = createGitlabProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await expect(
      provider.createIssue({ title: 't', body: 'b', assets: [], captureUrl: 'https://x', labels: [] }),
    ).rejects.toThrow(/status 500/);
  });

  it('id is gitlab', () => {
    const provider = createGitlabProvider({
      ...baseConfig,
      fetch: vi.fn(),
      getAuthorizationCode: () => Promise.resolve('code'),
    });
    expect(provider.id).toBe('gitlab');
  });

  it('falls back to the global fetch when none is injected', async () => {
    const globalFetch = vi.fn().mockResolvedValue(jsonResponse(issueCreated));
    vi.stubGlobal('fetch', globalFetch);
    try {
      const provider = createGitlabProvider({
        ...baseConfig,
        getAuthorizationCode: () => Promise.resolve('code'),
      });
      await provider.createIssue({ title: 't', body: 'b', assets: [], captureUrl: 'https://x', labels: [] });
      expect(globalFetch).toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
