import { parseRuleset, type RedactionRuleset } from '@htr/capture-core';
import type { ChromeStorage } from '../lib/chrome-adapter.js';

/** Key the managed policy stores the ruleset JSON under. */
export const MANAGED_RULESET_KEY = 'ruleset';

export const DEFAULT_REFRESH_MS = 5 * 60_000;

export type RulesetStore = {
  /** Current ruleset, or `null` before the first successful load. */
  current(): RedactionRuleset | null;
  /** Load once from `storage.managed`. Leaves `current()` unchanged on failure. */
  load(): Promise<void>;
  /** Start refreshing on a timer plus `storage.onChanged`. Idempotent. */
  startRefresh(): void;
  stopRefresh(): void;
};

/**
 * Loads the redaction ruleset from `chrome.storage.managed` (policy-pushed,
 * per spec §4.2) and keeps it fresh. A malformed or missing managed value
 * is fail-closed: `current()` stays whatever it was (`null` means "nothing
 * is allowed yet" — see `service-worker.ts`'s allow-list check).
 */
export function createRulesetStore(
  storage: ChromeStorage,
  refreshMs = DEFAULT_REFRESH_MS,
): RulesetStore {
  let ruleset: RedactionRuleset | null = null;
  let timer: ReturnType<typeof setInterval> | undefined;

  const onChanged = (changes: Record<string, unknown>): void => {
    if (MANAGED_RULESET_KEY in changes) void load();
  };

  async function load(): Promise<void> {
    const stored = await storage.managed.get(MANAGED_RULESET_KEY);
    const raw = stored[MANAGED_RULESET_KEY];
    if (raw === undefined) return;
    try {
      ruleset = parseRuleset(raw);
    } catch {
      // Fail closed: keep the last-known-good ruleset rather than adopt a
      // malformed one that might drop the origin-allow gate entirely.
    }
  }

  return {
    current: () => ruleset,
    load,
    startRefresh(): void {
      if (timer !== undefined) return;
      timer = setInterval(() => void load(), refreshMs);
      storage.onChanged.addListener(onChanged);
    },
    stopRefresh(): void {
      if (timer === undefined) return;
      clearInterval(timer);
      timer = undefined;
      storage.onChanged.removeListener(onChanged);
    },
  };
}
