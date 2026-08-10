import { describe, expect, it } from 'vitest';
import { hasTransport, type SyncTransport } from './transport.js';

describe('hasTransport', () => {
  it('is false for undefined', () => {
    expect(hasTransport(undefined)).toBe(false);
  });

  it('is true for a configured transport', () => {
    const transport: SyncTransport = {
      pushMutations: () => Promise.resolve([]),
      pullDeltas: () => Promise.resolve({ mutations: [], revision: 0, hasMore: false }),
      requestAssetUpload: () =>
        Promise.resolve({
          uploadUrl: '',
          objectKey: '',
          requiredHeaders: {},
          expiresAtUnixMs: 0,
        }),
      completeAssetUpload: () => Promise.resolve({ verified: true, manifestComplete: true, error: '' }),
    };
    expect(hasTransport(transport)).toBe(true);
  });
});
