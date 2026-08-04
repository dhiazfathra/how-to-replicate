import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  build: {
    target: 'esnext',
    rollupOptions: {
      output: {
        // One vendor chunk per npm package, so a dependency bump invalidates
        // only its own chunk's cache entry, not a shared vendor blob.
        manualChunks(id: string): string | undefined {
          if (!id.includes('node_modules')) return undefined;
          const parts = id.split('node_modules/')[1]?.split('/') ?? [];
          const name = parts[0]?.startsWith('@') ? `${parts[0]}/${parts[1]}` : parts[0];
          return name ? `vendor/${name}` : undefined;
        },
      },
    },
  },
});
