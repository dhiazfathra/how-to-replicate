/**
 * The locality label every provider carries. `selectProvider` is the only
 * consumer that trusts this field — it is derived by each provider's
 * factory, never accepted from caller configuration, because a caller could
 * otherwise mislabel a remote gateway as `'localhost'` and defeat the
 * local-only guarantee (ADR-007).
 */
export type ProviderTarget = 'localhost' | 'native-messaging' | 'remote';

export type CompletionRequest = {
  prompt: string;
  timeoutMs: number;
};

/**
 * Marks an `LlmProvider` as actually built by one of this package's
 * factories. Without this, `target` would be trustable-by-convention only —
 * any plain object literal could claim `target: 'localhost'` while its
 * `complete` posts to a remote gateway, and `selectProvider` would believe
 * it. Only `http-provider.ts` and `native-messaging-provider.ts` set this
 * key; `selectProvider` treats any provider missing it as `'remote'`
 * regardless of what `target` claims.
 */
export const providerBrand: unique symbol = Symbol('htr-llm-provider-brand');

export type LlmProvider = {
  name: string;
  target: ProviderTarget;
  complete(req: CompletionRequest): Promise<string>;
  readonly [providerBrand]: true;
};
