// Bundles the in-page nightly blur fixture (real capture-core blur code) into
// a single ES module the fixture page loads. Same pattern as `e2e/build.mjs`.
import * as esbuild from 'esbuild';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));

await esbuild.build({
  entryPoints: [path.join(here, 'fixture/entry.ts')],
  outfile: path.join(here, 'public/fixture.js'),
  bundle: true,
  format: 'esm',
  target: 'esnext',
  sourcemap: 'inline',
  logLevel: 'warning',
});
