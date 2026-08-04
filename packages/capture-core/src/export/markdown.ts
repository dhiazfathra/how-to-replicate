import type { Capture } from '../types/capture.js';
import { assertReady } from '../pipeline/gate.js';

/** A raw `|` inside a table cell would otherwise split it into extra columns. */
function escapeTableCell(value: string): string {
  return value.replace(/\|/g, '\\|');
}

/** Render a ready capture's `ReplicationDoc` to markdown. Throws on non-`ready`. */
export function renderMarkdown(capture: Capture): string {
  assertReady(capture);
  const doc = capture.doc;
  if (!doc) {
    throw new Error(`capture "${capture.id}" is ready but has no document`);
  }

  const lines: string[] = [`# ${doc.title}`, '', doc.summary, ''];

  if (capture.fidelity === 'degraded') {
    lines.push(
      '> **Fidelity: degraded.** ' +
        `${capture.withheldEventCount} events withheld by redaction policy.`,
      '',
    );
  }

  lines.push('## Steps', '');
  for (const step of doc.steps) {
    const refs = step.eventIds.map((id) => `\`${id}\``).join(', ');
    lines.push(`${step.n}. ${step.text} (events: ${refs})`);
  }
  lines.push('');

  lines.push('## Expected', '', doc.expected ?? '_not recorded_', '');
  lines.push('## Actual', '', doc.actual ?? '_not recorded_', '');

  lines.push('## Environment', '');
  lines.push('| Field | Value |', '| --- | --- |');
  lines.push(`| URL | ${escapeTableCell(capture.env.url)} |`);
  lines.push(`| User agent | ${escapeTableCell(capture.env.userAgent)} |`);
  lines.push(`| Platform | ${escapeTableCell(capture.env.platform)} |`);
  lines.push(`| Viewport | ${capture.env.viewport.w}x${capture.env.viewport.h} |`);
  lines.push(`| Device pixel ratio | ${capture.env.devicePixelRatio} |`);
  lines.push(`| Locale | ${escapeTableCell(capture.env.locale)} |`);
  lines.push(`| Timezone | ${escapeTableCell(capture.env.timezone)} |`);
  lines.push('');

  return lines.join('\n');
}
