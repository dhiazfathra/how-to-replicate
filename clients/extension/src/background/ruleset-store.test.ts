import { describe, expect, it, vi } from 'vitest';
import { createRulesetStore, MANAGED_RULESET_KEY } from './ruleset-store.js';
import type { ChromeStorage } from '../lib/chrome-adapter.js';

const validRuleset = {
  version: '1',
  rules: [{ id: 'a', class: 'origin-allow', origins: ['https://example.com'] }],
};

function fakeStorage(initial: Record<string, unknown> = {}): ChromeStorage {
  let value = initial;
  const listeners = new Set<(changes: Record<string, unknown>, area: string) => void>();
  return {
    managed: {
      get: (keys: string | string[] | null) => {
        void keys;
        return Promise.resolve(value);
      },
    },
    onChanged: {
      addListener: (l: (changes: Record<string, unknown>, area: string) => void) => listeners.add(l),
      removeListener: (l: (changes: Record<string, unknown>, area: string) => void) => listeners.delete(l),
    },
    // test-only hook, not part of the ChromeStorage surface
    __set(next: Record<string, unknown>) {
      value = next;
      for (const l of listeners) l(next, 'managed');
    },
  } as unknown as ChromeStorage & { __set(next: Record<string, unknown>): void };
}

describe('createRulesetStore', () => {
  it('starts with no ruleset until loaded', () => {
    const store = createRulesetStore(fakeStorage());
    expect(store.current()).toBeNull();
  });

  it('loads a valid ruleset from managed storage', async () => {
    const storage = fakeStorage({ [MANAGED_RULESET_KEY]: validRuleset });
    const store = createRulesetStore(storage);
    await store.load();
    expect(store.current()?.version).toBe('1');
  });

  it('leaves current() unchanged when the managed key is absent', async () => {
    const store = createRulesetStore(fakeStorage({}));
    await store.load();
    expect(store.current()).toBeNull();
  });

  it('fails closed: keeps the last-known-good ruleset on a malformed update', async () => {
    const storage = fakeStorage({ [MANAGED_RULESET_KEY]: validRuleset });
    const store = createRulesetStore(storage);
    await store.load();
    (storage as unknown as { __set(v: Record<string, unknown>): void }).__set({
      [MANAGED_RULESET_KEY]: { not: 'a ruleset' },
    });
    // onChanged listener isn't attached until startRefresh(); call load directly.
    await store.load();
    expect(store.current()?.version).toBe('1');
  });

  it('refreshes on a timer and on storage.onChanged, and stopRefresh tears both down', async () => {
    vi.useFakeTimers();
    const storage = fakeStorage({ [MANAGED_RULESET_KEY]: validRuleset });
    const store = createRulesetStore(storage, 1000);
    store.startRefresh();
    store.startRefresh(); // idempotent
    await vi.advanceTimersByTimeAsync(1000);
    expect(store.current()?.version).toBe('1');

    const updated = { version: '2', rules: [] };
    (storage as unknown as { __set(v: Record<string, unknown>): void }).__set({
      [MANAGED_RULESET_KEY]: updated,
    });
    await vi.waitFor(() => expect(store.current()?.version).toBe('2'));

    store.stopRefresh();
    store.stopRefresh(); // idempotent
    (storage as unknown as { __set(v: Record<string, unknown>): void }).__set({
      [MANAGED_RULESET_KEY]: { version: '3', rules: [] },
    });
    expect(store.current()?.version).toBe('2');
    vi.useRealTimers();
  });

  it('ignores unrelated storage changes', async () => {
    const storage = fakeStorage({ [MANAGED_RULESET_KEY]: validRuleset });
    const store = createRulesetStore(storage);
    store.startRefresh();
    await store.load();
    (storage as unknown as { __set(v: Record<string, unknown>): void }).__set({
      unrelatedKey: 'x',
    });
    expect(store.current()?.version).toBe('1');
    store.stopRefresh();
  });
});
