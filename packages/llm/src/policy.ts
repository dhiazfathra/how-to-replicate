import type { CompletionRequest, LlmProvider, ProviderTarget } from './provider.js';

export type LlmPolicy = {
  localOnly: boolean;
};

export type SelectedCompletion = {
  provider: LlmProvider;
  text: string;
};

/**
 * A provider whose `target` cannot be read is treated as `'remote'` — fail
 * closed, most-restrictive, never assume a provider of unknown locality is
 * safe to call under `localOnly`.
 */
function readTarget(provider: LlmProvider): ProviderTarget {
  const target = provider.target;
  return target === 'localhost' || target === 'native-messaging' ? target : 'remote';
}

/**
 * Tries providers in order and returns the first successful completion.
 * When `policy.localOnly` is set, the chain is filtered to
 * `'localhost'` / `'native-messaging'` targets *before* any provider is
 * tried — a remote provider is never reachable, whether it would have been
 * first in the chain or a fallback after every local provider failed.
 */
export async function selectProvider(
  policy: LlmPolicy,
  providers: LlmProvider[],
  req: CompletionRequest,
): Promise<SelectedCompletion | null> {
  const chain = policy.localOnly
    ? providers.filter((p) => readTarget(p) !== 'remote')
    : providers;

  for (const provider of chain) {
    try {
      const text = await provider.complete(req);
      return { provider, text };
    } catch {
      continue;
    }
  }

  return null;
}
