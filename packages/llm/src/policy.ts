import { providerBrand, type CompletionRequest, type LlmProvider, type ProviderTarget } from './provider.js';

export type LlmPolicy = {
  localOnly: boolean;
};

export type SelectedCompletion = {
  provider: LlmProvider;
  text: string;
};

/**
 * A provider whose `target` cannot be trusted is treated as `'remote'` —
 * fail closed, most-restrictive. That covers both an unreadable `target`
 * value and, more importantly, a provider missing the `providerBrand` key:
 * without the brand check a plain object literal could claim
 * `target: 'localhost'` without ever going through a real factory, which is
 * exactly the bypass `target` exists to prevent.
 */
function readTarget(provider: LlmProvider): ProviderTarget {
  if (provider[providerBrand] !== true) return 'remote';
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
