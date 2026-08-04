import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    passWithNoTests: true,
    coverage: {
      // `main.ts` files are pure wiring against the ambient `chrome`/`document`
      // globals (declared, not provided, outside a real extension host) —
      // every branch of actual behavior they call into lives in the sibling
      // modules they wire together, which are fully covered.
      exclude: ['src/background/main.ts', 'src/content/main.ts', 'src/offscreen/main.ts'],
    },
  },
});
