import js from '@eslint/js';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    // `e2e/public/harness.js` is the esbuild-bundled harness, not source.
    ignores: ['**/dist/**', '**/coverage/**', '**/node_modules/**', 'e2e/public/harness.js'],
  },
  js.configs.recommended,
  {
    files: ['packages/**/src/**/*.ts', 'clients/**/src/**/*.ts', 'e2e/**/*.ts'],
    extends: [...tseslint.configs.recommendedTypeChecked],
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    files: ['**/*.config.ts', 'scripts/*.mjs', 'e2e/*.mjs', '**/*.test.mjs'],
    extends: [...tseslint.configs.recommended],
    languageOptions: {
      parserOptions: {
        sourceType: 'module',
      },
    },
  },
);
