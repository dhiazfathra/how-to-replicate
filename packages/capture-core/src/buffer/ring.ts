export type RingBufferOptions = {
  maxEvents?: number;
  maxBytes?: number;
};

export type RingBuffer<T> = {
  push(event: T): T[];
  toArray(): T[];
  size(): number;
  byteSize(): number;
};

export const DEFAULT_MAX_EVENTS = 20_000;
export const DEFAULT_MAX_BYTES = 8 * 1024 * 1024;

/**
 * Fixed-capacity FIFO capped by count AND serialized byte size, whichever is
 * hit first. Oldest entries are evicted first. `push` returns whatever it
 * evicted so callers (e.g. Instant Replay) can count withheld events.
 */
export function createRingBuffer<T>(options: RingBufferOptions = {}): RingBuffer<T> {
  const maxEvents = options.maxEvents ?? DEFAULT_MAX_EVENTS;
  const maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;

  const items: T[] = [];
  const sizes: number[] = [];
  let totalBytes = 0;

  function measure(event: T): number {
    return new TextEncoder().encode(JSON.stringify(event)).byteLength;
  }

  return {
    push(event: T): T[] {
      const bytes = measure(event);
      items.push(event);
      sizes.push(bytes);
      totalBytes += bytes;

      const evicted: T[] = [];
      while (items.length > maxEvents || totalBytes > maxBytes) {
        evicted.push(items.shift() as T);
        totalBytes -= sizes.shift() as number;
      }
      return evicted;
    },
    toArray(): T[] {
      return [...items];
    },
    size(): number {
      return items.length;
    },
    byteSize(): number {
      return totalBytes;
    },
  };
}
