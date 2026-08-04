import { describe, expect, it, vi } from 'vitest';
import { TokenStore, type EncryptedRefreshToken, type LocalStorage, type SessionStorage } from './token-store.js';

function createSessionStorage(): SessionStorage {
  const store = new Map<string, string>();
  return {
    get: (key) => Promise.resolve(store.get(key)),
    set: (key, value) => {
      store.set(key, value);
      return Promise.resolve();
    },
    remove: (key) => {
      store.delete(key);
      return Promise.resolve();
    },
  };
}

/** Simulates chrome.storage.local: a plain object map that survives across TokenStore instances. */
function createLocalStorage(): LocalStorage {
  const store = new Map<string, EncryptedRefreshToken>();
  return {
    get: (key) => Promise.resolve(store.get(key)),
    set: (key, value) => {
      store.set(key, value);
      return Promise.resolve();
    },
    remove: (key) => {
      store.delete(key);
      return Promise.resolve();
    },
  };
}

describe('TokenStore access token', () => {
  it('round-trips the access token through session storage', async () => {
    const store = new TokenStore(createSessionStorage(), createLocalStorage());
    expect(await store.getAccessToken()).toBeUndefined();

    await store.setAccessToken('access-1');
    expect(await store.getAccessToken()).toBe('access-1');

    await store.clearAccessToken();
    expect(await store.getAccessToken()).toBeUndefined();
  });
});

describe('TokenStore refresh token encryption', () => {
  it('round-trips a refresh token through AES-GCM encryption', async () => {
    const store = new TokenStore(createSessionStorage(), createLocalStorage());
    await store.setRefreshToken('refresh-secret');

    expect(await store.getRefreshToken()).toBe('refresh-secret');
  });

  it('returns undefined when no refresh token is stored', async () => {
    const store = new TokenStore(createSessionStorage(), createLocalStorage());
    expect(await store.getRefreshToken()).toBeUndefined();
  });

  it('the key survives a simulated service-worker restart (new TokenStore, same local storage)', async () => {
    const local = createLocalStorage();
    const storeBeforeRestart = new TokenStore(createSessionStorage(), local);
    await storeBeforeRestart.setRefreshToken('survives-restart');

    const storeAfterRestart = new TokenStore(createSessionStorage(), local);
    expect(await storeAfterRestart.getRefreshToken()).toBe('survives-restart');
  });

  it('produces different IVs and ciphertext for two encryptions of the same plaintext', async () => {
    const local = createLocalStorage();
    const store = new TokenStore(createSessionStorage(), local);

    await store.setRefreshToken('same-plaintext');
    const first = await local.get('htr.tracker.refreshToken');

    await store.setRefreshToken('same-plaintext');
    const second = await local.get('htr.tracker.refreshToken');

    expect(first).toBeDefined();
    expect(second).toBeDefined();
    expect(first?.iv).not.toEqual(second?.iv);
    expect(new Uint8Array(first!.ciphertext)).not.toEqual(new Uint8Array(second!.ciphertext));
    // Both still decrypt to the same plaintext independently.
    expect(await store.getRefreshToken()).toBe('same-plaintext');
  });

  it('rotation re-encrypts under a newly generated key and discards the old one', async () => {
    const local = createLocalStorage();
    const store = new TokenStore(createSessionStorage(), local);

    await store.setRefreshToken('token-v1');
    const before = await local.get('htr.tracker.refreshToken');

    await store.rotateRefreshToken('token-v2');
    const after = await local.get('htr.tracker.refreshToken');

    expect(before?.key).not.toBe(after?.key);
    expect(await store.getRefreshToken()).toBe('token-v2');
  });

  it('clearRefreshToken removes the stored entry', async () => {
    const store = new TokenStore(createSessionStorage(), createLocalStorage());
    await store.setRefreshToken('to-be-cleared');
    await store.clearRefreshToken();
    expect(await store.getRefreshToken()).toBeUndefined();
  });
});

describe('TokenStore decryption failure modes', () => {
  it('discards the token and returns undefined on corrupt ciphertext', async () => {
    const local = createLocalStorage();
    const store = new TokenStore(createSessionStorage(), local);
    await store.setRefreshToken('will-be-corrupted');

    const entry = await local.get('htr.tracker.refreshToken');
    const corrupted = new Uint8Array(entry!.ciphertext);
    corrupted[0] = corrupted[0]! ^ 0xff;
    await local.set('htr.tracker.refreshToken', { ...entry!, ciphertext: corrupted.buffer });

    expect(await store.getRefreshToken()).toBeUndefined();
    expect(await local.get('htr.tracker.refreshToken')).toBeUndefined();
  });

  it('discards the token and returns undefined when the key is absent (structured-clone edge case)', async () => {
    const local = createLocalStorage();
    const store = new TokenStore(createSessionStorage(), local);
    await store.setRefreshToken('will-lose-key');

    const entry = await local.get('htr.tracker.refreshToken');
    await local.set('htr.tracker.refreshToken', {
      ...entry!,
      key: undefined as unknown as CryptoKey,
    });

    expect(await store.getRefreshToken()).toBeUndefined();
    expect(await local.get('htr.tracker.refreshToken')).toBeUndefined();
  });

  it('discards the token and returns undefined on a GCM tag mismatch (tampered IV)', async () => {
    const local = createLocalStorage();
    const store = new TokenStore(createSessionStorage(), local);
    await store.setRefreshToken('will-mismatch-tag');

    const entry = await local.get('htr.tracker.refreshToken');
    const tamperedIv = new Uint8Array(entry!.iv);
    tamperedIv[0] = tamperedIv[0]! ^ 0xff;
    await local.set('htr.tracker.refreshToken', { ...entry!, iv: tamperedIv });

    expect(await store.getRefreshToken()).toBeUndefined();
    expect(await local.get('htr.tracker.refreshToken')).toBeUndefined();
  });

  it('never falls back to plaintext or retries with a different key after failure', async () => {
    const local = createLocalStorage();
    const store = new TokenStore(createSessionStorage(), local);
    await store.setRefreshToken('secret-value');

    const entry = await local.get('htr.tracker.refreshToken');
    const corrupted = new Uint8Array(entry!.ciphertext);
    corrupted[0] = corrupted[0]! ^ 0xff;
    await local.set('htr.tracker.refreshToken', { ...entry!, ciphertext: corrupted.buffer });

    const decryptSpy = vi.spyOn(crypto.subtle, 'decrypt');
    const result = await store.getRefreshToken();

    expect(result).toBeUndefined();
    expect(decryptSpy).toHaveBeenCalledTimes(1);
    decryptSpy.mockRestore();
  });
});
