# @htr/sdk

A small, zero-dependency browser SDK a host application embeds to enrich its
captures with app-specific context — no extension required.

## Usage

```ts
import { htr } from '@htr/sdk';

htr.metadata({ userId: 'u-123', tenant: 'acme', buildSha, featureFlags: { newCheckout: true } });
```

- `metadata(input)` merges `input` into an accumulated context (last write
  wins per key across multiple calls — e.g. set `userId` at init, add
  `featureFlags` later) and returns the redacted snapshot that gets attached
  to `Capture.metadata`.
- `snapshot()` returns the current accumulated state without adding anything.
- Need an isolated instance (tests, multiple embeds on one page)? Use
  `createHtrSdk()` instead of the default `htr` singleton — each instance owns
  its own metadata and its own redactor.

Every capture produced this way carries `source: 'sdk'`.

## Redaction

`metadata()` is not a bypass. Every field passed in is scanned by the exact
same redaction engine Phase 0 built for events
(`@htr/capture-core`'s `createRedactor`) — spec §5.1 requires SDK-injected
metadata to be redaction-scanned like any other payload, and this routes
through the real engine rather than reimplementing the scan. A host
application that accidentally passes a patient name into `metadata()` will
not leak it: the matching field is replaced with a redaction marker, and the
rule that fired is recorded in `redaction.rulesApplied` (the same shape
`CaptureEvent.redaction` already uses elsewhere).

```ts
const snapshot = htr.metadata({ email: 'siti.rahayu@example.com' });
// snapshot.metadata.email === '[REDACTED:email]'
// snapshot.redaction === { rulesApplied: ['builtin:email'], fidelity: 'redacted' }
```

## Why this is small

The SDK has no policy channel of its own (no extension, no build-time
ruleset fetch), so it ships the built-in synthetic-PHI pattern library
(spec §18) as its redaction floor — the same patterns Phase 0's extension
ships. Everything is imported by submodule path
(`@htr/capture-core/src/redaction/engine.js`, never the package's main
barrel), so IndexedDB storage, the sync engine, and media/blur code never
end up in the bundle.

Measured with `pnpm check:bundle-size` (esbuild, minified, gzipped): **~2.8KB
gzipped / ~7.9KB raw**, gated in CI at an 8KB gzip budget
(`scripts/check-bundle-size.mjs`).

## Scripts

- `pnpm build` — bundles `src/index.ts` to `dist/htr.min.js` (esbuild).
- `pnpm check:bundle-size` — builds and fails if the gzipped bundle exceeds
  budget; run in CI on every push/PR.
