import { copyFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { defineConfig, type Plugin } from 'vite';

const root = import.meta.dirname;

/** Copies manifest.json + managed_schema.json into dist/ alongside the built entries. */
function copyManifest(): Plugin {
  return {
    name: 'copy-manifest',
    closeBundle(): void {
      for (const file of ['manifest.json', 'managed_schema.json']) {
        copyFileSync(resolve(root, file), resolve(root, 'dist', file));
      }
    },
  };
}

export default defineConfig({
  plugins: [copyManifest()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      input: {
        'background/main': resolve(root, 'src/background/main.ts'),
        'content/main': resolve(root, 'src/content/main.ts'),
      },
      output: {
        entryFileNames: '[name].js',
      },
    },
    target: 'esnext',
  },
});
