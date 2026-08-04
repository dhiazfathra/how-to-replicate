import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    projects: [
      {
        test: {
          name: 'root',
          environment: 'node',
          include: ['scripts/**/*.test.mjs'],
        },
      },
      'packages/*',
      'clients/*',
    ],
    coverage: {
      provider: 'v8',
      all: false,
      exclude: ['**/dist/**', '**/*.config.ts', '**/*.config.js'],
      thresholds: {
        lines: 100,
        functions: 100,
        branches: 100,
        statements: 100,
      },
    },
  },
});
