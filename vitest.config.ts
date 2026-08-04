import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    // `@vitest/coverage-v8` merges each isolated test file's independent V8
    // coverage session for this workspace's ~60 test files. Under the
    // default multi-worker `threads` pool that merge intermittently dropped
    // branch hits on small `clients/extension/src/offscreen/*.ts` modules
    // that are loaded from more than one test file (their own dedicated test
    // plus transitively via `recorder.test.ts` -> `recorder.ts`) — reported
    // as an uncovered "branch" on the module's own first line, with no
    // missing test behind it. Forcing a single worker removed the
    // cross-thread half of that nondeterminism and took the observed failure
    // rate from ~1-in-3 runs to rare; it did not provably reach zero (V8's
    // coverage API has known per-isolate merge gaps for tiny modules — see
    // vitest issues around `Profiler.takePreciseCoverage`). If this
    // resurfaces, the next lever is de-duplicating which test file exercises
    // these modules (mock `blur.js`/`frame-budget.js`/etc. in
    // `recorder.test.ts` so each module is only ever covered once), not
    // re-running the suite until it's green.
    fileParallelism: false,
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
      // Per-project `coverage.exclude` (e.g. `clients/*/vitest.config.ts`) is not
      // merged when projects run under this root config's `projects` array — v8
      // coverage config is resolved once, here. Client `main.ts` files are pure
      // wiring against ambient globals (`chrome.*`, `document`, `getDisplayMedia`)
      // that jsdom/node don't provide; every branch of actual behavior lives in
      // the sibling modules they wire together, which are fully covered and
      // exercised with fakes. Listed here, not in the per-project configs, so a
      // client that starts importing its `main.ts` from a test (as
      // `recording-link` now does, to keep an egress-safety regression test
      // honest) doesn't unpredictably flip the global threshold depending on
      // which test files a given run happens to load.
      exclude: [
        '**/dist/**',
        '**/*.config.ts',
        '**/*.config.js',
        'clients/recording-link/src/main.ts',
        'clients/extension/src/background/main.ts',
        'clients/extension/src/content/main.ts',
        'clients/extension/src/offscreen/main.ts',
        // `export type`-only modules (no runtime statements at all). v8's
        // coverage instrumentation is nondeterministic for these: depending on
        // module-graph/transform timing, a run sometimes reports them as a
        // 0/0-statement entry that the reporter renders as 0%, which is the
        // other half of this suite's "flaky coverage" — not a missed test, a
        // file with nothing to cover that shouldn't be in the report at all.
        'packages/capture-core/src/types/**',
      ],
      thresholds: {
        lines: 100,
        functions: 100,
        branches: 100,
        statements: 100,
      },
    },
  },
});
