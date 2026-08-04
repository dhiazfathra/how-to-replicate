import { describe, expect, it, vi } from 'vitest';
import { createHttpProvider } from './http-provider.js';

function jsonResponse(body: unknown, ok = true, status = 200): Response {
  return {
    ok,
    status,
    json: () => Promise.resolve(body),
  } as Response;
}

describe('createHttpProvider target derivation', () => {
  it.each([
    ['http://localhost:8080', 'localhost'],
    ['http://localhost.example.com', 'remote'],
    ['https://api.openai.com/v1', 'remote'],
    ['http://127.0.0.1:1234', 'localhost'],
    ['http://127.255.0.1', 'localhost'],
    ['http://[::1]:8080', 'localhost'],
    ['http://0.0.0.0:11434', 'localhost'],
    ['http://localhost.:8080', 'localhost'],
    ['http://[::ffff:127.0.0.1]:8080', 'localhost'],
    ['not a url', 'remote'],
  ] as const)('%s -> %s', (baseUrl, target) => {
    const provider = createHttpProvider({ baseUrl, model: 'x' });
    expect(provider.target).toBe(target);
  });
});

describe('createHttpProvider complete', () => {
  it('posts an OpenAI-compatible request and returns the message content', async () => {
    const fetch = vi.fn().mockResolvedValue(
      jsonResponse({ choices: [{ message: { content: 'hello' } }] }),
    );
    const provider = createHttpProvider({
      baseUrl: 'http://localhost:1234',
      apiKey: 'secret',
      model: 'gpt-x',
      fetch,
    });

    const result = await provider.complete({ prompt: 'p', timeoutMs: 1000 });

    expect(result).toBe('hello');
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('http://localhost:1234/chat/completions');
    expect((init.headers as Record<string, string>).authorization).toBe('Bearer secret');
    expect(JSON.parse(init.body as string)).toMatchObject({ model: 'gpt-x' });
  });

  it('omits the authorization header when no apiKey is given', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(jsonResponse({ choices: [{ message: { content: 'ok' } }] }));
    const provider = createHttpProvider({ baseUrl: 'http://localhost:1234', model: 'm', fetch });

    await provider.complete({ prompt: 'p', timeoutMs: 1000 });

    const [, init] = fetch.mock.calls[0] as [string, RequestInit];
    expect((init.headers as Record<string, string>).authorization).toBeUndefined();
  });

  it('throws on a non-ok response', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({}, false, 500));
    const provider = createHttpProvider({ baseUrl: 'http://localhost:1234', model: 'm', fetch });

    await expect(provider.complete({ prompt: 'p', timeoutMs: 1000 })).rejects.toThrow(/status 500/);
  });

  it('throws on a malformed response missing message content', async () => {
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ choices: [{}] }));
    const provider = createHttpProvider({ baseUrl: 'http://localhost:1234', model: 'm', fetch });

    await expect(provider.complete({ prompt: 'p', timeoutMs: 1000 })).rejects.toThrow(/malformed/);
  });

  it('aborts and rejects on timeout', async () => {
    const fetch = vi.fn().mockImplementation(
      (_url: string, init?: RequestInit) =>
        new Promise((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => {
            reject(new Error('aborted'));
          });
        }),
    );
    const provider = createHttpProvider({ baseUrl: 'http://localhost:1234', model: 'm', fetch });

    await expect(provider.complete({ prompt: 'p', timeoutMs: 5 })).rejects.toThrow();
  });

  it('uses the global fetch when none is injected', () => {
    const provider = createHttpProvider({ baseUrl: 'http://localhost:1234', model: 'm' });
    expect(provider.name).toBe('http:m');
  });
});
