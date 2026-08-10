#!/usr/bin/env node
// Bundles the SDK to a single minified ESM file for host applications to
// embed directly (script tag / CDN), rather than relying on host bundlers to
// resolve the `@htr/capture-core` subpath imports (workspace-internal paths
// that don't exist once this were ever published standalone). `bundle: true`
// inlines exactly the redaction submodules index.ts/ruleset.ts/types.ts pull
// in — never the capture-core barrel — which is what keeps this small; see
// src/index.ts and src/ruleset.ts for why those imports are subpaths.
import { build } from 'esbuild';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
export const ROOT = path.resolve(__dirname, '..');
export const ENTRY = path.join(ROOT, 'src/index.ts');
export const OUTFILE = path.join(ROOT, 'dist/htr.min.js');

export async function buildBundle() {
  await build({
    entryPoints: [ENTRY],
    outfile: OUTFILE,
    bundle: true,
    minify: true,
    treeShaking: true,
    format: 'esm',
    target: 'es2020',
    platform: 'browser',
  });
  return OUTFILE;
}

/* v8 ignore start -- CLI entry guard, exercised by pnpm build, not unit-testable without spawning a subprocess */
if (import.meta.url === `file://${process.argv[1]}`) {
  await buildBundle();
}
/* v8 ignore stop */
