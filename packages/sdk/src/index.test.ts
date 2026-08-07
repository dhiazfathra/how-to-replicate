import { describe, expect, it } from 'vitest';
import { createHtrSdk, htr } from './index.js';

// No `chrome.*`, no IndexedDB shim, no extension machinery of any kind is
// set up anywhere in this file — proving the SDK works standalone, embedded
// in a page that has none of Phase 0's browser-extension pieces installed.
describe('createHtrSdk (no extension installed)', () => {
  it('merges successive metadata() calls, last write wins per key', () => {
    const sdk = createHtrSdk();

    const first = sdk.metadata({ userId: 'u-1', tenant: 'acme' });
    expect(first.metadata).toEqual({ userId: 'u-1', tenant: 'acme' });

    const second = sdk.metadata({ tenant: 'acme-2', buildSha: 'abc123' });
    expect(second.metadata).toEqual({ userId: 'u-1', tenant: 'acme-2', buildSha: 'abc123' });
  });

  it('stamps source: sdk on every snapshot', () => {
    const sdk = createHtrSdk();
    expect(sdk.metadata({ userId: 'u-1' }).source).toBe('sdk');
    expect(sdk.snapshot().source).toBe('sdk');
  });

  it('snapshot() returns the accumulated state without adding anything', () => {
    const sdk = createHtrSdk();
    sdk.metadata({ userId: 'u-1' });
    expect(sdk.snapshot().metadata).toEqual({ userId: 'u-1' });
  });

  it('redacts a patient name passed into featureFlags/metadata rather than leaking it', () => {
    const sdk = createHtrSdk();
    const snapshot = sdk.metadata({ userId: 'u-1', note: 'siti.rahayu@example.com' });
    expect(snapshot.metadata.note).not.toContain('siti.rahayu@example.com');
    expect(snapshot.redaction.fidelity).toBe('redacted');
  });

  it('two instances do not share accumulated metadata', () => {
    const a = createHtrSdk();
    const b = createHtrSdk();
    a.metadata({ userId: 'from-a' });
    b.metadata({ userId: 'from-b' });
    expect(a.snapshot().metadata.userId).toBe('from-a');
    expect(b.snapshot().metadata.userId).toBe('from-b');
  });

  it('exports a ready-to-use default singleton', () => {
    expect(htr.snapshot().source).toBe('sdk');
  });
});
