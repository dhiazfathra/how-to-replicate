/**
 * Access/refresh token storage. Injected `SessionStorage` /
 * `LocalStorage` interfaces stand in for `chrome.storage.session` /
 * `chrome.storage.local` so this package holds no direct `chrome`
 * reference and stays node-testable.
 *
 * Refresh-token lifecycle (see task brief — this is a security-critical
 * spec, not left to implementer discretion):
 *  - key: non-extractable AES-GCM-256 `CryptoKey`, generated once per
 *    install, stored as a structured-cloneable value (never serialized to
 *    raw bytes) so it survives a service-worker restart.
 *  - nonce: fresh 96-bit random IV per encryption, stored alongside the
 *    ciphertext. Never derived, never reused.
 *  - rotation: every successful refresh discards the old key and
 *    re-encrypts under a freshly generated one. No key-history store.
 *  - decrypt failure of any kind (corrupt ciphertext, missing key, GCM tag
 *    mismatch) discards the stored refresh token and forces
 *    re-authorization. Never falls back to plaintext, never retries with a
 *    different key.
 */

export type SessionStorage = {
  get(key: string): Promise<string | undefined>;
  set(key: string, value: string): Promise<void>;
  remove(key: string): Promise<void>;
};

export type EncryptedRefreshToken = {
  key: CryptoKey;
  iv: Uint8Array;
  ciphertext: ArrayBuffer;
};

export type LocalStorage = {
  get(key: string): Promise<EncryptedRefreshToken | undefined>;
  set(key: string, value: EncryptedRefreshToken): Promise<void>;
  remove(key: string): Promise<void>;
};

const ACCESS_TOKEN_KEY = 'htr.tracker.accessToken';
const REFRESH_TOKEN_KEY = 'htr.tracker.refreshToken';

async function generateKey(): Promise<CryptoKey> {
  return crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']);
}

export class TokenStore {
  constructor(
    private readonly session: SessionStorage,
    private readonly local: LocalStorage,
  ) {}

  async getAccessToken(): Promise<string | undefined> {
    return this.session.get(ACCESS_TOKEN_KEY);
  }

  async setAccessToken(token: string): Promise<void> {
    await this.session.set(ACCESS_TOKEN_KEY, token);
  }

  async clearAccessToken(): Promise<void> {
    await this.session.remove(ACCESS_TOKEN_KEY);
  }

  /** Encrypts `refreshToken` under a freshly generated key, discarding whatever key preceded it. */
  async setRefreshToken(refreshToken: string): Promise<void> {
    const key = await generateKey();
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const ciphertext = await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv },
      key,
      new TextEncoder().encode(refreshToken),
    );
    await this.local.set(REFRESH_TOKEN_KEY, { key, iv, ciphertext });
  }

  /**
   * Returns the decrypted refresh token, or `undefined` if none is stored or
   * decryption fails in any way. A failure discards the stored entry so the
   * caller is forced back through re-authorization rather than retrying.
   */
  async getRefreshToken(): Promise<string | undefined> {
    const entry = await this.local.get(REFRESH_TOKEN_KEY);
    if (!entry) return undefined;
    try {
      const plaintext = await crypto.subtle.decrypt(
        { name: 'AES-GCM', iv: entry.iv as BufferSource },
        entry.key,
        entry.ciphertext,
      );
      return new TextDecoder().decode(plaintext);
    } catch {
      await this.local.remove(REFRESH_TOKEN_KEY);
      return undefined;
    }
  }

  /** Rotates the refresh token: re-encrypts under a brand-new key, discarding the old one (no history). */
  async rotateRefreshToken(newRefreshToken: string): Promise<void> {
    await this.setRefreshToken(newRefreshToken);
  }

  async clearRefreshToken(): Promise<void> {
    await this.local.remove(REFRESH_TOKEN_KEY);
  }
}
