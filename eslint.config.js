import js from '@eslint/js';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    ignores: ['**/dist/**', '**/coverage/**', '**/node_modules/**'],
  },
  js.configs.recommended,
  {
    files: ['packages/**/src/**/*.ts', 'clients/**/src/**/*.ts'],
    extends: [...tseslint.configs.recommendedTypeChecked],
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    files: ['**/*.config.ts', 'scripts/*.mjs', '**/*.test.mjs'],
    extends: [...tseslint.configs.recommended],
    languageOptions: {
      parserOptions: {
        sourceType: 'module',
      },
    },
  },
);
