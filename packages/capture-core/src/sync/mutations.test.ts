import { describe, expect, it } from 'vitest';
import type { Capture } from '../types/capture.js';
import {
  applyMutation,
  fieldForOp,
  getFieldValue,
  topLevelKeyOf,
  withFieldValue,
} from './mutations.js';

function makeCapture(): Capture {
  return {
    id: 'cap-1',
    workspaceId: null,
    projectId: null,
    source: 'sdk',
    state: 'ready',
    fidelity: 'full',
    createdAt: '2026-08-04T00:00:00.000Z',
    epoch: 0,
    env: {
      userAgent: 'x',
      platform: 'x',
      viewport: { w: 1, h: 1 },
      devicePixelRatio: 1,
      locale: 'en-US',
      timezone: 'UTC',
      url: 'https://example.com',
    },
    metadata: {},
    doc: null,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
  };
}

describe('mutations', () => {
  it('topLevelKeyOf routes doc.* to doc and metadata.* to metadata', () => {
    expect(topLevelKeyOf('doc.title')).toBe('doc');
    expect(topLevelKeyOf('doc.summary')).toBe('doc');
    expect(topLevelKeyOf('metadata.tags')).toBe('metadata');
    expect(topLevelKeyOf('metadata.assigneeUserId')).toBe('metadata');
    expect(topLevelKeyOf('metadata.comments')).toBe('metadata');
  });

  it('fieldForOp maps every op to its field', () => {
    expect(fieldForOp({ type: 'setTitle', title: 'x' })).toBe('doc.title');
    expect(fieldForOp({ type: 'setSummary', summary: 'x' })).toBe('doc.summary');
    expect(fieldForOp({ type: 'setTags', tags: [] })).toBe('metadata.tags');
    expect(fieldForOp({ type: 'assign', assigneeUserId: 'u1' })).toBe('metadata.assigneeUserId');
    expect(fieldForOp({ type: 'appendComment', commentId: 'c1', body: 'hi' })).toBe(
      'metadata.comments',
    );
  });

  it('getFieldValue defaults doc fields when doc is null', () => {
    const capture = makeCapture();
    expect(getFieldValue(capture, 'doc.title')).toBe('');
    expect(getFieldValue(capture, 'doc.summary')).toBe('');
  });

  it('getFieldValue defaults metadata fields when unset or wrong-typed', () => {
    const capture = makeCapture();
    expect(getFieldValue(capture, 'metadata.tags')).toEqual([]);
    expect(getFieldValue(capture, 'metadata.assigneeUserId')).toBe('');
    expect(getFieldValue(capture, 'metadata.comments')).toEqual([]);
  });

  it('getFieldValue reads real values once fields are actually set', () => {
    let capture = withFieldValue(makeCapture(), 'doc.summary', 'a real summary');
    capture = withFieldValue(capture, 'metadata.tags', ['a', 'b']);
    capture = withFieldValue(capture, 'metadata.assigneeUserId', 'u1');
    expect(getFieldValue(capture, 'doc.summary')).toBe('a real summary');
    expect(getFieldValue(capture, 'metadata.tags')).toEqual(['a', 'b']);
    expect(getFieldValue(capture, 'metadata.assigneeUserId')).toBe('u1');
  });

  it('withFieldValue creates doc when absent and preserves sibling fields', () => {
    const capture = makeCapture();
    const updated = withFieldValue(capture, 'doc.title', 'My Title');
    expect(updated.doc?.title).toBe('My Title');
    expect(updated.doc?.summary).toBe('');

    const withSummary = withFieldValue(updated, 'doc.summary', 'My Summary');
    expect(withSummary.doc?.title).toBe('My Title');
    expect(withSummary.doc?.summary).toBe('My Summary');
  });

  it('withFieldValue sets metadata fields without disturbing siblings', () => {
    const capture = withFieldValue(makeCapture(), 'metadata.tags', ['a']);
    const withAssignee = withFieldValue(capture, 'metadata.assigneeUserId', 'u1');
    expect(withAssignee.metadata.tags).toEqual(['a']);
    expect(withAssignee.metadata.assigneeUserId).toBe('u1');
  });

  it('applyMutation applies each op type', () => {
    let capture = makeCapture();
    capture = applyMutation(capture, { type: 'setTitle', title: 'T' });
    capture = applyMutation(capture, { type: 'setSummary', summary: 'S' });
    capture = applyMutation(capture, { type: 'setTags', tags: ['a', 'b'] });
    capture = applyMutation(capture, { type: 'assign', assigneeUserId: 'u1' });
    expect(capture.doc?.title).toBe('T');
    expect(capture.doc?.summary).toBe('S');
    expect(capture.metadata.tags).toEqual(['a', 'b']);
    expect(capture.metadata.assigneeUserId).toBe('u1');
  });

  it('applyMutation appends a comment onto existing comments', () => {
    let capture = applyMutation(makeCapture(), {
      type: 'appendComment',
      commentId: 'c1',
      body: 'first',
    });
    capture = applyMutation(capture, { type: 'appendComment', commentId: 'c2', body: 'second' });
    expect(capture.metadata.comments).toEqual([
      { id: 'c1', body: 'first' },
      { id: 'c2', body: 'second' },
    ]);
  });
});
