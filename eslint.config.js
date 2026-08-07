import js from '@eslint/js';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    // `e2e/public/harness.js` and `e2e-nightly/public/fixture.js` are
    // esbuild-bundled output, not source.
    ignores: [
      '**/dist/**',
      '**/coverage/**',
      '**/node_modules/**',
      'e2e/public/harness.js',
      'e2e-nightly/public/fixture.js',
    ],
  },
  js.configs.recommended,
  {
    files: ['packages/**/src/**/*.ts', 'clients/**/src/**/*.ts', 'e2e/**/*.ts', 'e2e-nightly/**/*.ts'],
    extends: [...tseslint.configs.recommendedTypeChecked],
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    files: [
      '**/*.config.ts',
      'scripts/*.mjs',
      'packages/*/scripts/*.mjs',
      'e2e/*.mjs',
      'e2e-nightly/*.mjs',
      '**/*.test.mjs',
    ],
    extends: [...tseslint.configs.recommended],
    languageOptions: {
      parserOptions: {
        sourceType: 'module',
      },
    },
  },
);
