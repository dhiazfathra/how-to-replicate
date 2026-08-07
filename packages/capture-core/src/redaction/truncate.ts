/** Max body size kept after truncation (spec §7.3): 32 KB, UTF-8 bytes. */
export const MAX_BODY_BYTES = 32 * 1024;

/** Content types whose bodies may be kept at all. Anything else is dropped
 * entirely — only status, timing, and size survive. */
export const ALLOWED_BODY_CONTENT_TYPES = [
  'application/json',
  'text/plain',
  'text/html',
  'application/x-www-form-urlencoded',
] as const;

export type TruncateResult = {
  body: string | null;
  bodyTruncated: boolean;
  bodyDropped: boolean;
};

/**
 * Apply the spec §7.3 body policy: bodies on a disallowed content type are
 * dropped outright; allowed bodies over 32 KB (UTF-8) are truncated to fit.
 */
export function truncateBody(body: string | null, contentType: string | null): TruncateResult {
  if (body === null) {
    return { body: null, bodyTruncated: false, bodyDropped: false };
  }

  const parts = (contentType ?? '').split(';');
  // `split` on a string always yields at least one element.
  const normalized = (parts[0] as string).trim().toLowerCase();
  if (!(ALLOWED_BODY_CONTENT_TYPES as readonly string[]).includes(normalized)) {
    return { body: null, bodyTruncated: false, bodyDropped: true };
  }

  const encoder = new TextEncoder();
  const bytes = encoder.encode(body);
  if (bytes.length <= MAX_BODY_BYTES) {
    return { body, bodyTruncated: false, bodyDropped: false };
  }

  const cut = safeUtf8CutIndex(bytes, MAX_BODY_BYTES);
  const truncated = new TextDecoder('utf-8').decode(bytes.subarray(0, cut));
  return { body: truncated, bodyTruncated: true, bodyDropped: false };
}

/**
 * Largest index `<= maxBytes` at which `bytes` can be cut without splitting a
 * multi-byte UTF-8 sequence. If the byte at `maxBytes` is a continuation byte
 * (`10xxxxxx`), the sequence that produced it started before `maxBytes` and
 * would still be incomplete there, so the whole sequence is dropped.
 */
function safeUtf8CutIndex(bytes: Uint8Array, maxBytes: number): number {
  const boundaryByte = bytes[maxBytes];
  if (boundaryByte === undefined || (boundaryByte & 0xc0) !== 0x80) {
    return maxBytes; // not mid-sequence: either past the end, or a fresh char starts here
  }
  let start = maxBytes;
  while (start > 0 && ((bytes[start] as number) & 0xc0) === 0x80) start--;
  return start;
}
