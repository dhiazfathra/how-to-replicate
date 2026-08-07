import { describe, expect, it } from 'vitest';
import { createRingBuffer, DEFAULT_MAX_BYTES, DEFAULT_MAX_EVENTS } from './ring.js';

describe('createRingBuffer', () => {
  it('defaults to 20000 events / 8MB', () => {
    expect(DEFAULT_MAX_EVENTS).toBe(20_000);
    expect(DEFAULT_MAX_BYTES).toBe(8 * 1024 * 1024);
  });

  it('accumulates pushes below both caps without eviction', () => {
    const ring = createRingBuffer<number>({ maxEvents: 10, maxBytes: 1024 });
    expect(ring.push(1)).toEqual([]);
    expect(ring.push(2)).toEqual([]);
    expect(ring.toArray()).toEqual([1, 2]);
    expect(ring.size()).toBe(2);
  });

  it('evicts oldest first by count', () => {
    const ring = createRingBuffer<number>({ maxEvents: 2, maxBytes: 1024 * 1024 });
    ring.push(1);
    ring.push(2);
    const evicted = ring.push(3);
    expect(evicted).toEqual([1]);
    expect(ring.toArray()).toEqual([2, 3]);
    expect(ring.size()).toBe(2);
  });

  it('evicts oldest first by bytes', () => {
    // Each string ~JSON-serializes to a fixed size; pick a byte cap that fits ~2.
    const item = 'x'.repeat(10); // serialized ~ 12 bytes ("x...x" quotes)
    const bytesEach = new TextEncoder().encode(JSON.stringify(item)).byteLength;
    const ring = createRingBuffer<string>({ maxEvents: 1000, maxBytes: bytesEach * 2 });
    ring.push(item);
    ring.push(item);
    const evicted = ring.push(item);
    expect(evicted).toEqual([item]);
    expect(ring.size()).toBe(2);
    expect(ring.byteSize()).toBeLessThanOrEqual(bytesEach * 2);
  });

  it('applies count and byte caps together, whichever binds first', () => {
    const item = 'y'.repeat(100);
    const bytesEach = new TextEncoder().encode(JSON.stringify(item)).byteLength;
    // Byte cap binds first (allows only 1) even though count cap would allow 5.
    const ring = createRingBuffer<string>({ maxEvents: 5, maxBytes: bytesEach + 1 });
    ring.push(item);
    const evicted = ring.push(item);
    expect(evicted).toEqual([item]);
    expect(ring.size()).toBe(1);
  });

  it('evicts an oversized single event rather than exceeding the byte cap', () => {
    const huge = 'z'.repeat(1000);
    const ring = createRingBuffer<string>({ maxEvents: 1000, maxBytes: 10 });
    const evicted = ring.push(huge);
    expect(evicted).toEqual([huge]);
    expect(ring.size()).toBe(0);
    expect(ring.byteSize()).toBe(0);
  });

  it('measures byte size on the serialized event, not object identity', () => {
    const ring = createRingBuffer<{ a: number }>({ maxEvents: 1000, maxBytes: 1024 * 1024 });
    ring.push({ a: 1 });
    const expected = new TextEncoder().encode(JSON.stringify({ a: 1 })).byteLength;
    expect(ring.byteSize()).toBe(expected);
  });
});
