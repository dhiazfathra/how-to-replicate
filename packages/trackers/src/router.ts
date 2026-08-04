import { assertReady, type Capture } from '@htr/capture-core';
import { deriveLabels } from './labels.js';
import type { IssueInput } from './provider.js';

/**
 * Builds the payload routed to a tracker. Calls `assertReady` first —
 * invariant 1: no unredacted capture is viewable, exportable, or routable,
 * and `ready` is the only state that satisfies that. Throws (refusing to
 * build a payload) for anything else.
 */
export function buildIssueInput(capture: Capture, captureUrl: string): IssueInput {
  assertReady(capture);
  const doc = capture.doc;
  if (!doc) {
    throw new Error(`capture "${capture.id}" is ready but has no replication doc`);
  }
  const body = [
    doc.summary,
    '',
    ...doc.steps.map((s) => `${s.n}. ${s.text}`),
    doc.expected ? `\nExpected: ${doc.expected}` : '',
    doc.actual ? `\nActual: ${doc.actual}` : '',
  ]
    .filter((line) => line !== '')
    .join('\n');

  const signatureText = [doc.title, doc.summary, ...doc.steps.map((s) => s.text)].join('\n');

  return {
    title: doc.title,
    body,
    assets: capture.assets,
    captureUrl,
    labels: deriveLabels(capture.projectId, signatureText),
  };
}
