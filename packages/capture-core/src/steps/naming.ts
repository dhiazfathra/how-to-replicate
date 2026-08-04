/**
 * A plain, serializable description of an interaction target. Callers in
 * `clients/` build this from a live DOM element; `capture-core` never touches
 * the DOM itself, so it stays framework- and platform-free.
 */
export type ElementDescriptor = {
  /** e.g. computed accessible name (aria-label, aria-labelledby, role text) */
  accessibleName: string | null;
  /** text of an associated <label> */
  labelText: string | null;
  /** the element's own text content, untrimmed */
  textContent: string | null;
  /** data-testid attribute */
  testId: string | null;
  /** a CSS selector identifying the element, e.g. from a selector builder */
  selector: string;
};

function nonEmpty(value: string | null): string | null {
  if (value === null) return null;
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

/**
 * Resolve a human-readable name for an interaction target, in priority
 * order: accessible name → associated label → trimmed text content →
 * data-testid → CSS selector (always present, so this never falls through).
 */
export function targetName(el: ElementDescriptor): string {
  return (
    nonEmpty(el.accessibleName) ??
    nonEmpty(el.labelText) ??
    nonEmpty(el.textContent) ??
    nonEmpty(el.testId) ??
    el.selector
  );
}
