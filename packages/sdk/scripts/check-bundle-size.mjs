#!/usr/bin/env node
// CI gate: builds the minified bundle and fails if its gzipped size exceeds
// the budget. Keeps "must not bundle the whole of capture-core" (the brief)
// enforced continuously, not just true on the day someone measured it.
import { gzipSync } from 'node:zlib';
import fs from 'node:fs';
import { buildBundle } from './build.mjs';

// Measured at ~2.8KB gzipped (7.9KB raw) for the redaction-only surface
// (patterns + engine + ruleset parsing). Budget leaves headroom for the
// pattern list to grow without silently ballooning into the rest of
// capture-core.
export const BUDGET_BYTES = 8 * 1024;

export async function checkBundleSize() {
  const outfile = await buildBundle();
  const raw = fs.readFileSync(outfile);
  const gzipped = gzipSync(raw);
  return { rawBytes: raw.length, gzipBytes: gzipped.length };
}

/* v8 ignore start -- CLI entry guard, exercised by pnpm check:bundle-size, not unit-testable without spawning a subprocess */
if (import.meta.url === `file://${process.argv[1]}`) {
  const { rawBytes, gzipBytes } = await checkBundleSize();
  console.log(`@htr/sdk bundle: ${rawBytes} bytes raw, ${gzipBytes} bytes gzipped (budget: ${BUDGET_BYTES})`);
  if (gzipBytes > BUDGET_BYTES) {
    console.error(`bundle size ${gzipBytes}B exceeds budget ${BUDGET_BYTES}B`);
    process.exit(1);
  }
}
/* v8 ignore stop */
