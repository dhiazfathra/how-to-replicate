import { describe, expect, it } from 'vitest';
import { newId, createIdFactory } from './identity.js';

describe('newId', () => {
  it('returns a 26-char ULID string', () => {
    const id = newId();
    expect(id).toHaveLength(26);
  });

  it('produces monotonically increasing IDs even when minted back to back', () => {
    const a = newId();
    const b = newId();
    expect(b > a).toBe(true);
  });
});

describe('createIdFactory', () => {
  it('mints monotonic IDs from an isolated factory', () => {
    const factory = createIdFactory();
    const a = factory();
    const b = factory();
    expect(b > a).toBe(true);
  });

  it('is monotonic under an identical seed time', () => {
    const factory = createIdFactory(1_700_000_000_000);
    const a = factory();
    const b = factory();
    expect(a).not.toBe(b);
    expect(b > a).toBe(true);
  });

  it('is isolated from other factories', () => {
    const f1 = createIdFactory();
    const f2 = createIdFactory();
    expect(f1()).not.toBe(f2());
  });
});
