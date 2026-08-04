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
});
