/** Max body size kept after truncation (spec §7.3): 32 KB, UTF-8 bytes. */
export const MAX_BODY_BYTES = 32 * 1024;

/** Content types whose bodies may be kept at all. Anything else is dropped
 * entirely — only status, timing, and size survive. */
export const ALLOWED_BODY_CONTENT_TYPES = [
  'application/json',
  'text/plain',
  'text/html',
  'application/x-www-form-urlencoded',
];

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
  if (!ALLOWED_BODY_CONTENT_TYPES.includes(normalized)) {
    return { body: null, bodyTruncated: false, bodyDropped: true };
  }

  const encoder = new TextEncoder();
  const bytes = encoder.encode(body);
  if (bytes.length <= MAX_BODY_BYTES) {
    return { body, bodyTruncated: false, bodyDropped: false };
  }

  const truncated = new TextDecoder('utf-8').decode(bytes.subarray(0, MAX_BODY_BYTES));
  return { body: truncated, bodyTruncated: true, bodyDropped: false };
}
