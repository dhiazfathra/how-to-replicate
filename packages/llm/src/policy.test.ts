import { describe, expect, it } from 'vitest';
import { selectProvider } from './policy.js';
import type { LlmProvider, ProviderTarget } from './provider.js';

function provider(
  name: string,
  target: ProviderTarget,
  complete: LlmProvider['complete'],
): LlmProvider {
  return { name, target, complete };
}

const ok = (text: string): LlmProvider['complete'] => () => Promise.resolve(text);
const fail = (): LlmProvider['complete'] => () => Promise.reject(new Error('boom'));

const req = { prompt: 'p', timeoutMs: 1000 };

describe('selectProvider', () => {
  it('returns the first provider that succeeds', async () => {
    const local = provider('local', 'localhost', ok('local-reply'));
    const remote = provider('remote', 'remote', ok('remote-reply'));

    const result = await selectProvider({ localOnly: false }, [local, remote], req);

    expect(result).toEqual({ provider: local, text: 'local-reply' });
  });

  it('falls back to the next provider when the first fails, when not local-only', async () => {
    const failing = provider('failing', 'localhost', fail());
    const remote = provider('remote', 'remote', ok('remote-reply'));

    const result = await selectProvider({ localOnly: false }, [failing, remote], req);

    expect(result).toEqual({ provider: remote, text: 'remote-reply' });
  });

  it('returns null when every provider fails', async () => {
    const a = provider('a', 'localhost', fail());
    const b = provider('b', 'remote', fail());

    const result = await selectProvider({ localOnly: false }, [a, b], req);

    expect(result).toBeNull();
  });

  it('never selects a remote provider under localOnly, even when it is first in the chain', async () => {
    const remote = provider('remote', 'remote', ok('remote-reply'));
    const local = provider('local', 'localhost', ok('local-reply'));

    const result = await selectProvider({ localOnly: true }, [remote, local], req);

    expect(result).toEqual({ provider: local, text: 'local-reply' });
  });

  it('never falls back to remote under localOnly once every local provider has failed', async () => {
    const remote = provider('remote', 'remote', ok('remote-reply'));
    const local1 = provider('local1', 'localhost', fail());
    const local2 = provider('local2', 'native-messaging', fail());

    const result = await selectProvider({ localOnly: true }, [remote, local1, local2], req);

    expect(result).toBeNull();
  });

  it('treats a provider with an unreadable target as remote and excludes it under localOnly', async () => {
    const unknown = provider('unknown', 'weird-target' as ProviderTarget, ok('should-not-run'));

    const result = await selectProvider({ localOnly: true }, [unknown], req);

    expect(result).toBeNull();
  });

  it('allows a provider with an unreadable target when not localOnly', async () => {
    const unknown = provider('unknown', 'weird-target' as ProviderTarget, ok('reply'));

    const result = await selectProvider({ localOnly: false }, [unknown], req);

    expect(result).toEqual({ provider: unknown, text: 'reply' });
  });

  it('returns null for an empty provider list', async () => {
    const result = await selectProvider({ localOnly: false }, [], req);
    expect(result).toBeNull();
  });
});
