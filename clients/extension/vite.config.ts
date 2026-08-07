import { copyFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { defineConfig, type Plugin } from 'vite';

const root = import.meta.dirname;

/** Copies manifest.json + managed_schema.json + offscreen.html into dist/ alongside the built entries. */
function copyManifest(): Plugin {
  return {
    name: 'copy-manifest',
    closeBundle(): void {
      for (const file of ['manifest.json', 'managed_schema.json', 'offscreen.html']) {
        copyFileSync(resolve(root, file), resolve(root, 'dist', file));
      }
    },
  };
}

// Only the two module contexts. The content script is deliberately NOT built
// here: it is a classic script (MV3 `content_scripts` has no module mode), so
// it cannot be code-split into shared `import`-ing chunks the way these two
// can. It gets its own single-file IIFE build — see `vite.content.config.ts`.
export default defineConfig({
  plugins: [copyManifest()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      input: {
        'background/main': resolve(root, 'src/background/main.ts'),
        'offscreen/main': resolve(root, 'src/offscreen/main.ts'),
      },
      output: {
        entryFileNames: '[name].js',
      },
    },
    target: 'esnext',
  },
});
