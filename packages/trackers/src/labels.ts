import type { IssueLabel } from './provider.js';

/**
 * Signature patterns checked against the replication doc's title/summary/steps
 * text. Order matters only for output order, not matching — every pattern
 * that hits produces a label.
 */
const ERROR_SIGNATURES: Array<{ label: string; pattern: RegExp }> = [
  { label: 'error:null-pointer', pattern: /null(?:pointer)?(?:reference)? exception|cannot read propert/i },
  { label: 'error:timeout', pattern: /timed? ?out|timeout/i },
  { label: 'error:network', pattern: /network error|ECONNREFUSED|fetch failed|failed to fetch/i },
  { label: 'error:permission', pattern: /permission denied|unauthorized|forbidden|403|401/i },
  { label: 'error:crash', pattern: /crash(?:ed)?|segfault|panic:/i },
];

/**
 * Derives structured labels from a capture's `projectId` and any error
 * signatures detected in free text (typically the rendered doc's title,
 * summary, and step text concatenated by the caller).
 */
export function deriveLabels(projectId: string | null, text: string): IssueLabel[] {
  const labels: IssueLabel[] = [];
  if (projectId) {
    labels.push({ name: `project:${projectId}`, source: 'project' });
  }
  for (const { label, pattern } of ERROR_SIGNATURES) {
    if (pattern.test(text)) {
      labels.push({ name: label, source: 'error-signature' });
    }
  }
  return labels;
}
