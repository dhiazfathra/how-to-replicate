import { describe, expect, it, vi } from 'vitest';
import type { Capture, CaptureState, ReplicationDoc } from '@htr/capture-core';
import { createSlackProvider, resolveSlackBinding, type SlackBinding } from './slack.js';
import { buildIssueInput } from './router.js';
import slackToken from './fixtures/slack-token.json' with { type: 'json' };
import slackTokenError from './fixtures/slack-token-error.json' with { type: 'json' };
import messagePosted from './fixtures/slack-message-posted.json' with { type: 'json' };
import messageError from './fixtures/slack-message-error.json' with { type: 'json' };

function jsonResponse(body: unknown, ok = true, status = 200): Response {
  return { ok, status, json: () => Promise.resolve(body) } as Response;
}

const baseConfig = {
  clientId: 'client-id',
  redirectUri: 'https://app.example.com/oauth/callback',
  channelId: 'C0123DEMO',
  getAccessToken: () => Promise.resolve('token'),
  getAssetUrl: () => Promise.resolve('https://assets.example.com/signed/video.webm'),
};

describe('createSlackProvider authorize', () => {
  it('exchanges the code for tokens using code_verifier, no client_secret', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(slackToken));
    let capturedAuthorizeUrl = '';
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: (authorizeUrl) => {
        capturedAuthorizeUrl = authorizeUrl;
        return Promise.resolve('returned-auth-code');
      },
    });

    const result = await provider.authorize();

    expect(capturedAuthorizeUrl).toContain('code_challenge_method=S256');
    expect(capturedAuthorizeUrl).toContain('slack.com/oauth/v2/authorize');
    expect(result).toEqual({
      accessToken: slackToken.access_token,
      refreshToken: slackToken.refresh_token,
      expiresInSeconds: slackToken.expires_in,
    });
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('https://slack.com/api/oauth.v2.access');
    const body = JSON.parse(init.body as string) as Record<string, unknown>;
    expect(body.code).toBe('returned-auth-code');
    expect(body).toHaveProperty('code_verifier');
    expect(body).not.toHaveProperty('client_secret');
  });

  it('throws when the token exchange HTTP call fails', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({}, false, 400));
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await expect(provider.authorize()).rejects.toThrow(/status 400/);
  });

  it('throws when Slack returns ok:false for the token exchange', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(slackTokenError));
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await expect(provider.authorize()).rejects.toThrow(/invalid_grant/);
  });

  it('handles a token response with no refresh_token/expires_in', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ ok: true, access_token: 'only-access' }));
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    const result = await provider.authorize();

    expect(result).toEqual({ accessToken: 'only-access', refreshToken: null, expiresInSeconds: null });
  });
});

describe('createSlackProvider createIssue', () => {
  const input = {
    title: 'Repro: crash on submit',
    body: [
      'The form crashes when submitted twice.',
      '',
      '1. Click submit',
      '2. Observe a network error',
      '\nExpected: Form submits once.',
      'Actual: App throws a network error.',
    ].join('\n'),
    assets: [],
    captureUrl: 'https://app.example.com/capture/01H',
    labels: [{ name: 'project:demo', source: 'project' as const }],
  };

  it('posts block-kit blocks (not a wall of markdown) with a deep link', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(messagePosted));
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    const result = await provider.createIssue(input);

    expect(result).toEqual({
      url: `https://slack.com/archives/${messagePosted.channel}/p${messagePosted.ts.replace('.', '')}`,
      id: messagePosted.ts,
    });
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('https://slack.com/api/chat.postMessage');
    expect((init.headers as Record<string, string>).authorization).toBe('Bearer token');
    const body = JSON.parse(init.body as string) as { channel: string; blocks: Array<{ type: string }> };
    expect(body.channel).toBe('C0123DEMO');
    expect(body.blocks[0]).toEqual({ type: 'header', text: { type: 'plain_text', text: input.title } });
    expect(body.blocks.length).toBeGreaterThan(1);
    expect(body.blocks.some((b) => b.type === 'header')).toBe(true);
    expect(JSON.stringify(body.blocks)).toContain('https://app.example.com/capture/01H');
    // Steps and summary must not be collapsed into one giant markdown blob.
    expect(body.blocks.filter((b) => b.type === 'section').length).toBeGreaterThan(1);
  });

  it('posts an inline video link block for a small video asset', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(messagePosted));
    const getAssetUrl = vi.fn().mockResolvedValue('https://assets.example.com/signed/small.webm');
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAssetUrl,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await provider.createIssue({
      ...input,
      assets: [
        {
          id: 'a1',
          captureId: 'c1',
          kind: 'video',
          mimeType: 'video/webm',
          sizeBytes: 1024,
          chunkCount: 1,
          sha256: 'x',
        },
      ],
    });

    expect(getAssetUrl).toHaveBeenCalled();
    const [, init] = fetch.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string) as { blocks: Array<Record<string, unknown>> };
    const videoBlock = body.blocks.find(
      (b) => JSON.stringify(b).includes('small.webm') && b.type === 'section',
    );
    expect(videoBlock).toBeDefined();
  });

  it('posts a signed-link download button for a large video asset', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(messagePosted));
    const getAssetUrl = vi.fn().mockResolvedValue('https://assets.example.com/signed/large.webm');
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAssetUrl,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await provider.createIssue({
      ...input,
      assets: [
        {
          id: 'a1',
          captureId: 'c1',
          kind: 'video',
          mimeType: 'video/webm',
          sizeBytes: 200 * 1024 * 1024,
          chunkCount: 200,
          sha256: 'x',
        },
      ],
    });

    const [, init] = fetch.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string) as { blocks: Array<Record<string, unknown>> };
    const actionsBlock = body.blocks.find((b) => b.type === 'actions');
    expect(actionsBlock).toBeDefined();
    expect(JSON.stringify(actionsBlock)).toContain('large.webm');
  });

  it('omits the labels context block when there are no labels', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(messagePosted));
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await provider.createIssue({ ...input, labels: [] });

    const [, init] = fetch.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string) as { blocks: Array<Record<string, unknown>> };
    expect(body.blocks.some((b) => b.type === 'context')).toBe(false);
  });

  it('renders only the actual field when expected is absent from the body', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(messagePosted));
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await provider.createIssue({
      ...input,
      body: ['Summary.', '', '1. Click submit', '\nActual: App throws a network error.'].join('\n'),
    });

    const [, init] = fetch.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string) as { blocks: Array<Record<string, unknown>> };
    const fieldsBlock = body.blocks.find((b) => 'fields' in b);
    expect(fieldsBlock).toBeDefined();
    expect(JSON.stringify(fieldsBlock)).not.toContain('Expected');
    expect(JSON.stringify(fieldsBlock)).toContain('Actual');
  });

  it('throws when the post-message HTTP call fails', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({}, false, 500));
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await expect(provider.createIssue(input)).rejects.toThrow(/status 500/);
  });

  it('throws when Slack returns ok:false for the post message', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(messageError));
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    await expect(provider.createIssue(input)).rejects.toThrow(/channel_not_found/);
  });

  it('id is slack', () => {
    const provider = createSlackProvider({
      ...baseConfig,
      fetch: vi.fn(),
      getAuthorizationCode: () => Promise.resolve('code'),
    });
    expect(provider.id).toBe('slack');
  });

  it('falls back to the global fetch when none is injected', async () => {
    const globalFetch = vi.fn().mockResolvedValue(jsonResponse(messagePosted));
    vi.stubGlobal('fetch', globalFetch);
    try {
      const provider = createSlackProvider({
        ...baseConfig,
        getAuthorizationCode: () => Promise.resolve('code'),
      });
      await provider.createIssue(input);
      expect(globalFetch).toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe('resolveSlackBinding', () => {
  const bindings: SlackBinding[] = [
    { id: 'b1', workspaceId: 'w1', provider: 'slack', config: { projectId: 'demo', channelId: 'C1' } },
    { id: 'b2', workspaceId: 'w1', provider: 'slack', config: { projectId: 'other', channelId: 'C2' } },
  ];

  it('resolves the binding matching the project', () => {
    expect(resolveSlackBinding(bindings, 'other')).toEqual(bindings[1]);
  });

  it('throws when no binding matches the project', () => {
    expect(() => resolveSlackBinding(bindings, 'missing')).toThrow(/no binding for project "missing"/);
  });
});

describe('Slack path refuses a non-ready capture before building any payload', () => {
  const doc: ReplicationDoc = {
    title: 'Repro: crash on submit',
    summary: 'The form crashes when submitted twice.',
    steps: [{ n: 1, text: 'Click submit', eventIds: ['e1'], tVideo: 0 }],
    expected: 'Form submits once.',
    actual: 'App throws a network error.',
    generator: 'deterministic',
    generatorModel: null,
  };

  function makeCapture(overrides: Partial<Capture> = {}): Capture {
    return {
      id: 'cap-1',
      workspaceId: null,
      projectId: 'demo',
      source: 'extension',
      state: 'ready',
      fidelity: 'full',
      createdAt: '2026-08-04T00:00:00.000Z',
      epoch: 0,
      env: {
        userAgent: 'test-agent',
        platform: 'test',
        viewport: { w: 100, h: 100 },
        devicePixelRatio: 1,
        locale: 'en-US',
        timezone: 'UTC',
        url: 'https://example.com',
      },
      metadata: {},
      doc,
      assets: [],
      withheldEventCount: 0,
      sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
      ...overrides,
    };
  }

  const nonReady: CaptureState[] = ['recording', 'redacting', 'composing', 'failed', 'expired'];
  it.each(nonReady)(
    'buildIssueInput (shared by every tracker, Slack included) refuses state %s',
    (state) => {
      const fetch = vi.fn();
      const provider = createSlackProvider({
        ...baseConfig,
        fetch,
        getAuthorizationCode: () => Promise.resolve('code'),
      });

      expect(() => buildIssueInput(makeCapture({ state }), 'https://x')).toThrow(/not ready/);
      // The gate throws before any IssueInput exists, so the provider is never reached.
      expect(fetch).not.toHaveBeenCalled();
      void provider;
    },
  );

  it('proceeds to build Slack blocks once the shared gate is satisfied', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse(messagePosted));
    const provider = createSlackProvider({
      ...baseConfig,
      fetch,
      getAuthorizationCode: () => Promise.resolve('code'),
    });

    const issueInput = buildIssueInput(makeCapture(), 'https://app.example.com/capture/cap-1');
    const result = await provider.createIssue(issueInput);

    expect(result.id).toBe(messagePosted.ts);
  });
});
