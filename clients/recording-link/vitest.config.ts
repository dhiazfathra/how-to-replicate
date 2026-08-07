import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'jsdom',
    coverage: {
      // Wiring against real `getDisplayMedia`/`MediaRecorder`/DOM globals
      // (not provided by jsdom) — every branch of actual behavior lives in
      // `capture-flow.ts`, which is exercised with fakes and fully covered.
      exclude: ['src/main.ts'],
    },
  },
});
