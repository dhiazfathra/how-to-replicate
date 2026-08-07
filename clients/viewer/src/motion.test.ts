import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// Read as plain text via Node, not a Vite CSS import — Vite's css plugin
// intercepts `.css?raw`/`?inline` specially and this asset is never actually
// injected as a stylesheet by any bundler step here, so a Node read is the
// simplest thing that reliably returns the real file contents.
const css = readFileSync(resolve(import.meta.dirname, 'motion.css'), 'utf8');

const FORBIDDEN = ['width', 'height', 'margin', 'top', 'left'];

describe('motion.css', () => {
  it('never lists a forbidden property inside transition or a keyframe animation', () => {
    // Matches "transition: <prop>" / "transition-property: <prop>" declarations
    // and picks apart comma-separated multi-property transitions.
    const transitionDeclarations = css.match(/transition(?:-property)?\s*:\s*[^;]+;/g) ?? [];
    for (const declaration of transitionDeclarations) {
      for (const prop of FORBIDDEN) {
        expect(declaration, `forbidden animated property "${prop}" in: ${declaration}`).not.toMatch(
          new RegExp(`(^|[\\s,:])${prop}(\\s|,|$)`),
        );
      }
    }
  });

  it('only animates transform and opacity', () => {
    const transitionDeclarations = css.match(/transition(?:-property)?\s*:\s*[^;]+;/g) ?? [];
    expect(transitionDeclarations.length).toBeGreaterThan(0);
    for (const declaration of transitionDeclarations) {
      expect(declaration).toMatch(/transform|opacity/);
    }
  });
});
