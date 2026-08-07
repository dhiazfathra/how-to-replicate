import { describe, expect, it } from 'vitest';
import { decodeChunk, encodeChunk } from './chunk-codec.js';

describe('chunk-codec', () => {
  it('round-trips a Uint8Array through base64', async () => {
    const original = new Uint8Array([0, 1, 2, 254, 255, 128]);
    const encoded = await encodeChunk(original);

    expect(typeof encoded).toBe('string');
    expect(decodeChunk(encoded)).toEqual(original);
  });

  it('round-trips a Blob through base64', async () => {
    const original = new Uint8Array([10, 20, 30]);
    const blob = new Blob([original]);

    const encoded = await encodeChunk(blob);

    expect(typeof encoded).toBe('string');
    expect(decodeChunk(encoded)).toEqual(original);
  });

  it('produces a value that survives JSON serialization untouched — the actual constraint being fixed', async () => {
    const original = new Uint8Array([1, 2, 3]);
    const encoded = await encodeChunk(original);

    // A Blob/Uint8Array sent raw over chrome.runtime.sendMessage arrives as
    // `{}` or an index-keyed object on the other side because sendMessage
    // JSON-serializes its payload. A JSON-safe string must survive that
    // round trip byte-for-byte.
    const overWire = JSON.parse(JSON.stringify({ data: encoded })) as { data: string };
    expect(decodeChunk(overWire.data)).toEqual(original);
  });

  it('round-trips an empty chunk', async () => {
    const encoded = await encodeChunk(new Uint8Array([]));
    expect(decodeChunk(encoded)).toEqual(new Uint8Array([]));
  });
});
