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

export type LlmProvider = {
  name: string;
  target: ProviderTarget;
  complete(req: CompletionRequest): Promise<string>;
};
