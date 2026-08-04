/**
 * Structural subset of DOM interfaces the content scripts touch. Kept
 * hand-rolled (rather than relying on `lib.dom` globals directly in
 * function signatures) so every DOM-touching function takes its target
 * through a parameter — testable with plain object literals, no jsdom.
 */

export type ElementLike = {
  tagName: string;
  getAttribute(name: string): string | null;
  closest(selector: string): ElementLike | null;
  getBoundingClientRect(): RectLike;
  textContent: string | null;
  querySelector(selector: string): ElementLike | null;
  matches(selector: string): boolean;
};

export type RectLike = { x: number; y: number; width: number; height: number };
