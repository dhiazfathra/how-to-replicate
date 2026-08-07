import type { Capture } from '../types/capture.js';
import type { ReplicationDoc } from '../types/doc.js';

/** A single appended comment, stored under `capture.metadata.comments`. */
export type Comment = { id: string; body: string };

// The closed operation set mirrored from proto/sync/v1/sync.proto `Mutation.op`.
// No generic setter — a new mutable field means a new variant here, deliberately.
export type SetTitleOp = { type: 'setTitle'; title: string };
export type SetSummaryOp = { type: 'setSummary'; summary: string };
export type SetTagsOp = { type: 'setTags'; tags: string[] };
export type AssignOp = { type: 'assign'; assigneeUserId: string };
export type AppendCommentOp = { type: 'appendComment'; commentId: string; body: string };

export type MutationOp = SetTitleOp | SetSummaryOp | SetTagsOp | AssignOp | AppendCommentOp;

/** Wire shape mirroring proto `Mutation` — what actually gets pushed to sync-gateway. */
export type Mutation = {
  id: string; // client-minted ULID
  captureId: string;
  op: MutationOp;
  clientT: number; // ms offset from capture.epoch, advisory only
};

/**
 * The granular field a mutation op touches. Distinct from the top-level
 * `keyof Capture` the store persists under (`doc`/`metadata`) — `doc.title`
 * and `doc.summary` share a top-level key but must never clobber each other
 * during rollback/replay, hence the finer key here.
 */
export type FieldKey =
  | 'doc.title'
  | 'doc.summary'
  | 'metadata.tags'
  | 'metadata.assigneeUserId'
  | 'metadata.comments';

export type FieldValue = string | string[] | Comment[];

/** The top-level `Capture` key that persisting a `FieldKey` writes through. */
export function topLevelKeyOf(field: FieldKey): 'doc' | 'metadata' {
  return field.startsWith('doc.') ? 'doc' : 'metadata';
}

export function fieldForOp(op: MutationOp): FieldKey {
  switch (op.type) {
    case 'setTitle':
      return 'doc.title';
    case 'setSummary':
      return 'doc.summary';
    case 'setTags':
      return 'metadata.tags';
    case 'assign':
      return 'metadata.assigneeUserId';
    case 'appendComment':
      return 'metadata.comments';
  }
}

function emptyDoc(): ReplicationDoc {
  return {
    title: '',
    summary: '',
    steps: [],
    expected: null,
    actual: null,
    generator: 'deterministic',
    generatorModel: null,
  };
}

/** Read a capture's current value for a granular field, with the type's zero value as default. */
export function getFieldValue(capture: Capture, field: FieldKey): FieldValue {
  switch (field) {
    case 'doc.title':
      return capture.doc?.title ?? '';
    case 'doc.summary':
      return capture.doc?.summary ?? '';
    case 'metadata.tags': {
      const tags = capture.metadata.tags;
      return Array.isArray(tags) ? (tags as string[]) : [];
    }
    case 'metadata.assigneeUserId': {
      const assignee = capture.metadata.assigneeUserId;
      return typeof assignee === 'string' ? assignee : '';
    }
    case 'metadata.comments': {
      const comments = capture.metadata.comments;
      return Array.isArray(comments) ? (comments as unknown as Comment[]) : [];
    }
  }
}

/** Pure setter for a granular field — merges into `doc`/`metadata` without disturbing siblings. */
export function withFieldValue(capture: Capture, field: FieldKey, value: FieldValue): Capture {
  switch (field) {
    case 'doc.title':
      return { ...capture, doc: { ...(capture.doc ?? emptyDoc()), title: value as string } };
    case 'doc.summary':
      return { ...capture, doc: { ...(capture.doc ?? emptyDoc()), summary: value as string } };
    case 'metadata.tags':
      return { ...capture, metadata: { ...capture.metadata, tags: value } };
    case 'metadata.assigneeUserId':
      return { ...capture, metadata: { ...capture.metadata, assigneeUserId: value } };
    case 'metadata.comments':
      return {
        ...capture,
        metadata: { ...capture.metadata, comments: value },
      };
  }
}

/** Apply one mutation op to a capture, returning the updated capture (pure). */
export function applyMutation(capture: Capture, op: MutationOp): Capture {
  switch (op.type) {
    case 'setTitle':
      return withFieldValue(capture, 'doc.title', op.title);
    case 'setSummary':
      return withFieldValue(capture, 'doc.summary', op.summary);
    case 'setTags':
      return withFieldValue(capture, 'metadata.tags', op.tags);
    case 'assign':
      return withFieldValue(capture, 'metadata.assigneeUserId', op.assigneeUserId);
    case 'appendComment': {
      const existing = getFieldValue(capture, 'metadata.comments') as Comment[];
      // Idempotent under reapplication: comment IDs are client-minted ULIDs,
      // already unique, so a comment already present (e.g. replayed by
      // pull() after the client's own flush()) is not appended twice.
      if (existing.some((c) => c.id === op.commentId)) return capture;
      // Sorted by ULID rather than local application order: ULIDs are
      // chronologically monotonic, and clients apply their own comment
      // optimistically (before push) while a concurrent peer's comment only
      // arrives later via pull — so raw append order diverges between
      // clients even though they've applied the same set of comments.
      // Sorting by ID gives every client the same final order regardless of
      // arrival order.
      const merged = [...existing, { id: op.commentId, body: op.body }];
      merged.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
      return withFieldValue(capture, 'metadata.comments', merged);
    }
  }
}
