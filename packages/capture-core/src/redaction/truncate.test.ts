import { describe, expect, it } from 'vitest';
import { MAX_BODY_BYTES, truncateBody } from './truncate.js';

describe('truncateBody', () => {
  it('passes through a null body', () => {
    expect(truncateBody(null, 'application/json')).toEqual({
      body: null,
      bodyTruncated: false,
      bodyDropped: false,
    });
  });

  it('drops the body when content type is not on the allow-list', () => {
    expect(truncateBody('{}', 'application/octet-stream')).toEqual({
      body: null,
      bodyTruncated: false,
      bodyDropped: true,
    });
  });

  it('drops the body when content type is missing', () => {
    expect(truncateBody('some text', null)).toEqual({
      body: null,
      bodyTruncated: false,
      bodyDropped: true,
    });
  });

  it('ignores content-type parameters (charset etc.) when checking the allow-list', () => {
    const body = 'hello';
    expect(truncateBody(body, 'text/plain; charset=utf-8')).toEqual({
      body,
      bodyTruncated: false,
      bodyDropped: false,
    });
  });

  it.each(['application/json', 'text/plain', 'text/html', 'application/x-www-form-urlencoded'])(
    'allows content type %s',
    (contentType) => {
      const result = truncateBody('ok', contentType);
      expect(result.bodyDropped).toBe(false);
    },
  );

  it('keeps a body of exactly 32 KB untruncated', () => {
    const body = 'a'.repeat(MAX_BODY_BYTES);
    const result = truncateBody(body, 'text/plain');
    expect(result).toEqual({ body, bodyTruncated: false, bodyDropped: false });
  });

  it('truncates a body one byte over 32 KB', () => {
    const body = 'a'.repeat(MAX_BODY_BYTES + 1);
    const result = truncateBody(body, 'text/plain');
    expect(result.bodyTruncated).toBe(true);
    expect(result.bodyDropped).toBe(false);
    expect(result.body).toHaveLength(MAX_BODY_BYTES);
    expect(new TextEncoder().encode(result.body ?? '').length).toBe(MAX_BODY_BYTES);
  });

  it('never splits a multi-byte character straddling the exact 32 KB cut', () => {
    // '€' is 3 UTF-8 bytes (0xE2 0x82 0xAC). Padding to MAX_BODY_BYTES - 1
    // ASCII bytes puts the '€' starting at the last included byte, so the
    // cut lands inside it (after its 1st byte).
    const body = 'a'.repeat(MAX_BODY_BYTES - 1) + '€';
    const result = truncateBody(body, 'text/plain');
    expect(result.bodyTruncated).toBe(true);
    expect(result.body).not.toContain('�');
    const bytes = new TextEncoder().encode(result.body ?? '');
    expect(bytes.length).toBeLessThanOrEqual(MAX_BODY_BYTES);
    // The dangling '€' must have been dropped whole, not partially decoded.
    expect(result.body).toBe('a'.repeat(MAX_BODY_BYTES - 1));
  });

  it('keeps a body whose multi-byte character lands exactly on the boundary', () => {
    // Pad so the '€' starts right where MAX_BODY_BYTES ends: the boundary
    // byte is the start of a *new* character (or past the end), not a
    // continuation byte, so nothing should be trimmed beyond the plain
    // over-length case.
    const body = 'a'.repeat(MAX_BODY_BYTES) + '€';
    const result = truncateBody(body, 'text/plain');
    expect(result.bodyTruncated).toBe(true);
    expect(result.body).toBe('a'.repeat(MAX_BODY_BYTES));
    expect(result.body).not.toContain('�');
  });
});
