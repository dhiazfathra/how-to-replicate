import { resolve } from 'node:path';
import { defineConfig } from 'vite';

const root = import.meta.dirname;

/**
 * Second, separate build for the content script.
 *
 * MV3 `content_scripts` are injected as **classic** scripts — there is no
 * `type: "module"` for them the way there is for `background.service_worker`.
 * A content script emitted as ESM (with `import ... from "../assets/…"` at the
 * top, which is what rollup produces as soon as any module is shared with
 * another entry) fails to parse in the page and the script silently never
 * runs. So this entry is built on its own, as a single self-contained IIFE
 * with no imports at all.
 *
 * Runs after the main build (which owns `emptyOutDir`) and only adds
 * `dist/content/main.js` to it.
 */
export default defineConfig({
  build: {
    outDir: 'dist',
    emptyOutDir: false,
    target: 'esnext',
    rollupOptions: {
      input: resolve(root, 'src/content/main.ts'),
      output: {
        format: 'iife',
        entryFileNames: 'content/main.js',
        inlineDynamicImports: true,
      },
    },
  },
});
