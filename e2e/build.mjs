// Bundles the in-page e2e harness (real capture-core + real extension
// modules) into a single ES module the demo page loads. Run by Playwright's
// `webServer` command before the static server starts.
import * as esbuild from 'esbuild';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));

await esbuild.build({
  entryPoints: [path.join(here, 'harness/entry.ts')],
  outfile: path.join(here, 'public/harness.js'),
  bundle: true,
  format: 'esm',
  target: 'esnext',
  sourcemap: 'inline',
  logLevel: 'warning',
});
