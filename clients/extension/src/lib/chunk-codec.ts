/**
 * `chrome.runtime.sendMessage` JSON-serializes its payload — a `Blob` or
 * `Uint8Array` crossing that boundary arrives on the other side as `{}` or an
 * index-keyed object, not the bytes it started as. Base64 is the JSON-safe
 * wire representation for a video chunk; encode here (offscreen side, before
 * `sendMessage`) and decode in `offscreen-relay.ts` (service-worker side,
 * after `onMessage`) so `capture-core`'s `pushVideoChunk(Uint8Array | Blob)`
 * contract never has to know the wire format changed.
 */
export async function encodeChunk(data: Uint8Array | Blob): Promise<string> {
  const bytes = data instanceof Blob ? new Uint8Array(await data.arrayBuffer()) : data;
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

export function decodeChunk(base64: string): Uint8Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}
