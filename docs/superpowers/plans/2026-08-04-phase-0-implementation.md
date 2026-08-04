# Phase 0 Implementation Plan

**Derives from:** [design spec](../specs/2026-08-04-how-to-replicate-design.md) §19 "Phase 0 — client only, zero new server-side code"
**Date:** 2026-08-04

Phase 0 only. Phases 1–3 (Go services, sync, MCP, CLI, iOS) are out of scope for
this plan and no code for them may be written.

---

## Global Constraints

Every task is bound by these. A violation is a failed review, not a nit.

### Invariants (from CLAUDE.md — breaking 1 or 2 is a compliance incident)

1. No unredacted capture is viewable, exportable, or routable. The gate is
   `state === 'ready'`.
2. No unblurred frame exists in a persisted artifact. Blur feeds `MediaRecorder`,
   not the player. Missed frame budget degrades to screenshot-only, never to
   unblurred video.
3. LLM steps cite event IDs that exist, or they are dropped.
4. Lost fidelity is visible — `fidelity: 'degraded'`, `withheldEventCount`.
5. `packages/capture-core` depends on nothing in `clients/`.

### Conventions

- **Local-first.** The UI reads the local observable store. Never add a fetch on a
  path where data is already local. Mutations apply locally and synchronously.
- **Event timestamps are ms offsets from `capture.epoch`**, never wall-clock. Do
  not introduce wall-clock comparisons inside a capture.
- **Client-minted ULIDs** for all identity, monotonic so sort order needs no
  tiebreaker. No server round trip to create anything.
- **Animate only `transform` and `opacity`.** Never `width`, `height`, `margin`,
  `top`, or `left`.
- **No Phase 0 service of our own.** The client may call APIs that already exist
  (GitHub, GitLab, an LLM gateway, a localhost model server). It may not call a
  service we would have to operate.
- **Redaction runs before the write to IndexedDB, never after**, and on ingest into
  the Instant Replay ring buffer, not at capture finalize.
- **Fail closed.** An unapplicable redaction rule drops the event and increments
  `withheldEventCount`. A redaction failure sets `state = 'failed'` and the capture
  is not viewable.

### Engineering standards

- **100% coverage of branches and edge cases** (`v8` provider, thresholds set to
  100 in `vitest.config.ts`). No `/* v8 ignore */` without a one-line justification
  comment.
- **Lint clean** — `pnpm lint` must exit 0 before a task is reported DONE.
- **Typecheck clean** — `pnpm typecheck` must exit 0. TypeScript `strict: true`.
- Every task ends with a real commit. Conventional commits:
  `<type>(<scope>): <summary>`.
- No new runtime dependency beyond the ones this plan names. If a task believes it
  needs one, report `DONE_WITH_CONCERNS` and say why instead of adding it.

### Stack decisions (already made — do not re-decide)

| Concern | Decision |
|---|---|
| Package manager | `pnpm` workspaces, `pnpm-workspace.yaml` |
| Language | TypeScript `strict`, ESM only (`"type": "module"`), `target: "esnext"` |
| Test runner | Vitest, `environment: 'node'` for `capture-core`, `jsdom` where DOM is needed |
| IndexedDB in tests | `fake-indexeddb` |
| IndexedDB wrapper | `idb` |
| ULID | `ulid` (`monotonicFactory`) |
| Lint | ESLint flat config (`eslint.config.js`) + `typescript-eslint` |
| Bundler | Vite (plain, multi-entry; no `@crxjs/vite-plugin`) |
| UI | React 19 + a hand-rolled per-field observable (no state-management dependency) |
| CI | GitHub Actions, Node 24 |

### Repository layout (spec §24)

```text
clients/
  extension/          MV3 extension (TS)
  viewer/             capture viewer
  recording-link/     no-login capture page
packages/
  capture-core/       event model, redaction, step generator, storage, store
  llm/                provider interface + HTTP and native-messaging impls
  trackers/           provider interface + GitHub, GitLab impls
docs/
```

---

## Task 1: Monorepo scaffold, tooling, and CI

Create the workspace every later task builds in. No product logic.

**Deliverables**

- `package.json` (root, private, `"type": "module"`, `packageManager` pinned to the
  installed pnpm) with scripts: `lint`, `typecheck`, `test`, `test:coverage`,
  `build`, `check:deps`.
- `pnpm-workspace.yaml` covering `packages/*` and `clients/*`.
- **`pnpm-lock.yaml`, committed.** CI runs `pnpm install --frozen-lockfile`, which
  fails on a clean checkout without it. Generate it in this task and commit it;
  the verification step below is what proves it is complete.
- `tsconfig.base.json`: `strict: true`, `target: "esnext"`, `module: "esnext"`,
  `moduleResolution: "bundler"`, `noUncheckedIndexedAccess: true`,
  `exactOptionalPropertyTypes: true`, `verbatimModuleSyntax: true`,
  `isolatedModules: true`.
- `vitest.config.ts` at root using Vitest workspace/projects so one `pnpm test`
  runs every package. Coverage provider `v8`, thresholds `lines/functions/branches/statements: 100`.
- `eslint.config.js` — flat config, `typescript-eslint` recommended-type-checked.
- Empty but valid workspace packages so `pnpm install` succeeds:
  `packages/capture-core`, `packages/llm`, `packages/trackers`,
  `clients/extension`, `clients/viewer`, `clients/recording-link`. Each gets a
  `package.json` (name `@htr/<dir>`), a `tsconfig.json` extending the base, and
  `src/index.ts` exporting nothing yet.
- `scripts/check-deps.mjs` — enforces invariant 5. Walk every file under
  `packages/capture-core/src`, fail with a non-zero exit and a listing if any
  import specifier resolves into `clients/` or names `@htr/extension`,
  `@htr/viewer`, `@htr/recording-link`. Also fail if `capture-core` imports any
  `chrome.*` or browser-extension API. Unit-test this script — it is CI logic and
  is covered by the 100% requirement.
- `.github/workflows/ci.yml` — on push and pull_request: setup pnpm + Node 24,
  `pnpm install --frozen-lockfile`, then `pnpm lint`, `pnpm typecheck`,
  `pnpm check:deps`, `pnpm test:coverage`. Every step must be blocking.
- `.gitignore` additions for `node_modules`, `dist`, `coverage`.

**Verification:** `pnpm install --frozen-lockfile && pnpm lint && pnpm typecheck && pnpm check:deps && pnpm test:coverage` all exit 0. Use the frozen form, not a bare `pnpm install` — it is what CI runs, and it is the only form that proves the committed lockfile is complete.

---

## Task 2: capture-core — event model, identity, and time

The typed foundation. Types plus the small runtime pieces that mint identity and
compute time. No redaction, no storage.

**Deliverables** in `packages/capture-core/src/`:

- `types/capture.ts` — transcribe verbatim from spec §5.1:

  ```ts
  type CaptureState =
    | 'recording' | 'redacting' | 'composing' | 'ready' | 'failed' | 'expired';

  type Capture = {
    id: string;
    workspaceId: string | null;
    projectId: string | null;
    source: 'extension' | 'recording-link' | 'sdk' | 'cli' | 'ios';
    state: CaptureState;
    fidelity: 'full' | 'degraded';
    createdAt: string;
    epoch: number;
    env: EnvSnapshot;
    metadata: Record<string, JsonValue>;
    doc: ReplicationDoc | null;
    assets: AssetRef[];
    withheldEventCount: number;
    sync: {
      revision: number;
      lastPushedAt: string | null;
      manifestComplete: boolean;
      dirtyFields: string[];
    };
  };
  ```

  `sync` is inert in Phase 0 — present in the type, initialized to
  `{ revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] }`,
  never read or written by Phase 0 logic.

- `types/event.ts` — transcribe verbatim from spec §5.2:

  ```ts
  type CaptureEvent = {
    id: string;
    captureId: string;
    t: number;
    kind: 'console' | 'network' | 'interaction' | 'navigation'
        | 'lifecycle' | 'annotation';
    payload: ConsolePayload | NetworkPayload | InteractionPayload
           | NavigationPayload | LifecyclePayload | AnnotationPayload;
    redaction: {
      rulesApplied: string[];
      fidelity: 'full' | 'redacted' | 'dropped';
    };
  };
  ```

  Define all six payload types. Minimum shapes:
  - `ConsolePayload`: `{ level: 'log'|'info'|'warn'|'error'|'debug'; text: string; stack: string | null }`
  - `NetworkPayload`: `{ method: string; url: string; status: number | null; requestHeaders: Record<string,string>; responseHeaders: Record<string,string>; requestBody: string | null; responseBody: string | null; bodyTruncated: boolean; bodyDropped: boolean; durationMs: number | null; sizeBytes: number | null }`
  - `InteractionPayload`: `{ type: 'click'|'input'|'keydown'|'scroll'|'submit'; targetName: string; targetSelector: string; url: string; value: string | null }`
  - `NavigationPayload`: `{ from: string | null; to: string; trigger: 'load'|'pushstate'|'popstate'|'replacestate'|'hashchange' }`
  - `LifecyclePayload`: `{ transition: string; detail: string | null }`
  - `AnnotationPayload`: `{ text: string }`

- `types/doc.ts` — `ReplicationDoc` and `Step` verbatim from spec §5.3.
- `types/asset.ts` — `AssetRef` (`{ id, captureId, kind: 'video'|'screenshot'|'har', mimeType, sizeBytes, chunkCount, sha256 }`), `EnvSnapshot`
  (`{ userAgent, platform, viewport: {w,h}, devicePixelRatio, locale, timezone, url }`), `JsonValue`.
- `identity.ts` — `newId(): string` backed by `ulid`'s `monotonicFactory`, created
  once at module scope so IDs are monotonic across a process. Export
  `createIdFactory(seedTime?)` for tests.
- `time.ts` — `createClock()` returning `{ epoch: number; now(): number }` where
  `epoch` is `performance.timeOrigin + performance.now()` at construction and
  `now()` returns the ms offset from that epoch. Accept an injectable
  `PerformanceLike` so tests never depend on the ambient clock. Reject a negative
  offset by clamping to 0 and document why in a comment.
- `index.ts` re-exporting all of the above.

**Tests:** every branch. Monotonic ID ordering under identical timestamps; clock
offset arithmetic including the clamp; type-level tests are not required but
runtime guards are.

---

## Task 3: capture-core — redaction engine

The compliance core (spec §7). This is the single most consequential module in the
repository. Fail closed everywhere.

**Deliverables** in `packages/capture-core/src/redaction/`:

- `ruleset.ts` — the versioned ruleset type and its parser:

  ```ts
  type RuleClass =
    | 'field-path' | 'header' | 'pattern' | 'dom-selector' | 'video-blur' | 'origin-allow';

  type RedactionRule =
    | { id: string; class: 'field-path'; pointer: string }        // RFC 6901-ish, `*` wildcard segment
    | { id: string; class: 'header'; name: string }               // case-insensitive
    | { id: string; class: 'pattern'; pattern: string; flags?: string; label: string }
    | { id: string; class: 'dom-selector'; selector: string }
    | { id: string; class: 'video-blur'; selector: string }
    | { id: string; class: 'origin-allow'; origins: string[] };

  type RedactionRuleset = {
    version: string;
    rules: RedactionRule[];
  };
  ```

  `parseRuleset(input: unknown): RedactionRuleset` throws on anything malformed —
  never silently drops an unparseable rule, because a dropped rule is a hole.
  Invalid regex source is a parse failure, not a skipped rule.

- `engine.ts` — `createRedactor(ruleset)` returning:

  ```ts
  type Redactor = {
    redactEvent(event: CaptureEvent): RedactionOutcome;
    isOriginAllowed(origin: string): boolean;
    blurSelectors(): string[];
  };

  type RedactionOutcome =
    | { fidelity: 'full' | 'redacted'; event: CaptureEvent }
    | { fidelity: 'dropped'; ruleId: string; reason: string };
  ```

  Behaviour, all of it mandatory:
  - Redaction is **pure and total**: given the same event and ruleset it returns
    the same outcome, and it never throws. Any internal error is caught and
    converted to a drop — an error must never let an event through unredacted.
    A drop always carries both fields the type demands: when a specific rule
    caused it, its `ruleId`; when the engine itself failed, the reserved
    `ruleId: 'engine:internal-error'` and a **sanitized** `reason` naming the
    failing stage only. The reason string must never quote payload content —
    an error message that echoes the value it choked on is a leak wearing a
    diagnostic hat.
  - **field-path** — walk JSON request/response bodies by pointer; `*` matches one
    path segment. Replace the matched value with `'[REDACTED]'`. A body that does
    not parse as JSON is not exempt: it falls through to pattern scanning.
  - **header** — case-insensitive name match on request and response headers;
    value replaced with `'[REDACTED]'`.
  - **pattern** — applied to every string reachable in the payload, **keys as
    well as values** (console text, URLs, header values, body strings,
    interaction values, annotation text, and every JSON object key along the
    way). Replace the match with `'[REDACTED:<label>]'`.
    Keys need explicit handling because redacting one can collide with a
    sibling: `{"0812...": 1, "0813...": 2}` would collapse to one entry. So a
    key rewrite is **collision-safe** — on collision, suffix the replacement
    with the key's ordinal position (`'[REDACTED:phone-id]#2'`) rather than
    overwriting. Dropping one of two colliding entries is silent data loss;
    merging them is worse, because it fabricates a payload that never existed.
  - **dom-selector** — applied to captured DOM text carried in interaction
    payloads (`targetName`, `value`): a payload whose `targetSelector` matches a
    masked selector has its text fields replaced with `'[REDACTED]'`.
  - **origin-allow** — `isOriginAllowed` returns false for any origin not listed.
    If the ruleset has no `origin-allow` rule, **nothing is allowed** — fail
    closed, not open.
  - **video-blur** — `blurSelectors()` returns the selectors; the engine does not
    itself touch pixels.
  - If any rule's evaluation is impossible (bad selector, unreachable pointer
    target of the wrong type, regex execution error), the **event is dropped**.
  - `rulesApplied` on the returned event lists exactly the rule IDs that fired,
    in ruleset order, deduplicated.

- Built-in pattern library in `patterns.ts` covering, with these exact labels:
  `nik` (Indonesian NIK — 16 digits), `bpjs` (13 digits), `mrn`, `phone-id`
  (Indonesian phone), `email`, `dob` (ISO and `DD/MM/YYYY`). Exported as ready-made
  `RedactionRule[]` so a ruleset can include them by spreading.

- `truncate.ts` — bodies truncated to **32 KB** (`32 * 1024` bytes, UTF-8) with
  `bodyTruncated: true`; content types not on the allow-list
  (`application/json`, `text/plain`, `text/html`, `application/x-www-form-urlencoded`)
  have bodies dropped entirely with `bodyDropped: true`, keeping only status,
  timing, and size (spec §7.3).

**Tests:** every rule class, every fail-closed path, the no-origin-rule case, the
throw-becomes-drop path, truncation at exactly 32 KB and one byte over.

---

## Task 4: The golden synthetic-PHI corpus

The most important test asset in the repository (spec §18). It gates every PR.

**Deliverables**

- `packages/capture-core/test/corpus/` — fixtures containing **synthetic** PHI
  only. Never real data. Cover, at minimum:
  - Indonesian NIK, BPJS number, MRN, DOB (both formats), Indonesian phone, email,
    and Indonesian patient names.
  - Placed across: JSON request bodies (nested, arrayed, deeply nested),
    JSON response bodies, request headers, response headers, URLs and query
    strings, console log text and stack traces, interaction `targetName` and
    `value`, navigation URLs, and annotation text.
  - Adversarial shapes: PHI split across a truncation boundary, PHI in a
    non-JSON body, PHI in a content type not on the allow-list, PHI in a key
    rather than a value, **two sibling keys whose redacted forms collide**,
    unicode-escaped PHI, and PHI in a URL fragment.
    The key-position fixtures are covered by Task 3's collision-safe key
    rewriting. If that rewriting is not implemented, these fixtures fail —
    which is the intended outcome, not a fixture to delete.
- `packages/capture-core/test/corpus/index.ts` — loads fixtures and exports the
  list of **forbidden strings** (every synthetic PHI literal used).
- `packages/capture-core/test/redaction-corpus.test.ts` — runs each fixture event
  through the redactor with the standard ruleset and asserts **zero leakage**: no
  forbidden string appears anywhere in the serialized outcome (including
  `rulesApplied`, including dropped-event metadata, and including **object keys**,
  so serialize with a walker that visits keys rather than relying on
  `JSON.stringify` alone). Dropping counts as a pass; leaking does not.
- A companion assertion that key redaction is **non-destructive**: for the
  colliding-sibling fixture, assert the redacted object still has the same number
  of keys as the original. A rewrite that silently merges two entries passes a
  naive leak check while fabricating a payload that never existed.
- A companion assertion that the corpus is non-trivial: fail the test if the
  forbidden-string list is empty or if any fixture produces `fidelity: 'full'`
  when it contains PHI — a fixture that no rule touches is a hole in the ruleset,
  not a pass.
- `.github/workflows/ci.yml`: add a distinct, blocking `redaction-corpus` job
  running only this test file, so a leak is visible as its own red check.

**Constraint:** do not weaken, skip, or mark any part of this flaky. If it fails,
the code is wrong.

---

## Task 5: capture-core — storage and the local budget

Spec §9. IndexedDB via `idb`, chunked assets, refuse-to-record at cap.

**Deliverables** in `packages/capture-core/src/storage/`:

- `schema.ts` — object stores: `captures` (key `id`), `events`
  (key `id`, index `by-capture` on `[captureId, t]`), `assets` (key `id`, index
  `by-capture`), `asset_chunks` (key `[assetId, seq]`), `rulesets` (key `version`).
- `db.ts` — `openCaptureDb(name?)` with versioned upgrade, injectable
  `indexedDB` factory so tests use `fake-indexeddb`.
- `repository.ts` — `CaptureRepository` with: `putCapture`, `getCapture`,
  `listCaptures`, `appendEvents(captureId, events[])` (single transaction),
  `readEvents(captureId)` (ordered by `t` then `id`), `putAssetChunked(assetRef, blob, chunkBytes)`,
  `readAsset(assetId)`, `deleteCapture(id)` (cascades events, assets, chunks).
  Assets persist as chunked records — default chunk **1 MB** — never one enormous
  value.
- `budget.ts`:
  - `requestPersistence(storage)` → calls `navigator.storage.persist()` before the
    first capture; injectable `StorageManager`.
  - `checkBudget({ estimate, captureCount, projectedBytes, limits })` returning
    `{ ok: true } | { ok: false; reason: 'quota' | 'capture-cap' | 'projected-overflow' }`.
  - Defaults: **2 GB** and **40 captures**, both overridable via a policy object.
  - **Phase 0 evicts nothing.** There is no LRU code path in this task. At the cap
    the result is a refusal the caller surfaces as an export-or-delete prompt.
    Silently deleting the only copy of a bug report is unacceptable.

**Tests:** `fake-indexeddb` throughout. Cascade deletes, chunk round-trip for a
blob larger than one chunk, ordering guarantees, every `checkBudget` branch,
persistence request when the API is absent.

---

## Task 6: capture-core — observable store and the Instant Replay ring buffer

**Deliverables**

- `packages/capture-core/src/store/observable.ts` — a minimal, dependency-free
  per-field observable (spec §16 "granular observables"):
  `createObservable<T>(initial)` → `{ get(), set(next), subscribe(fn): () => void }`,
  and `createRecordStore<T>()` holding per-entity, per-field observables so a
  delta re-renders one cell rather than a list. Subscribers fire synchronously on
  `set`. Setting a value equal by `Object.is` notifies nobody.
- `packages/capture-core/src/store/capture-store.ts` — `createCaptureStore(repo)`:
  hydrates from the repository, exposes `captures` as observable records, and
  applies mutations **locally and synchronously** before any persistence. No
  Phase 0 mutation queue — persistence is a direct repository write.
  **Persistence-failure contract:** the local write happens first, so a failed
  repository write would otherwise leave the UI showing a value that vanishes on
  reload. It does **not** roll back — rollback would discard the user's edit for
  a fault they did not cause. Instead the field is marked dirty with the error,
  the store exposes it (`persistError`), and the viewer surfaces it. Lost
  persistence is visible, on the same principle as invariant 4.
- `packages/capture-core/src/buffer/ring.ts` — `createRingBuffer({ maxEvents, maxBytes })`
  capped by count **and** bytes, defaults **20 000 events / 8 MB**, evicting oldest
  first. `push(event)` returns the evicted events. Byte size is measured on the
  serialized event.
- `packages/capture-core/src/buffer/instant-replay.ts` — `createInstantReplay({ redactor, ring, clock })`.
  `ingest(rawEvent)` **redacts on ingest** and only then pushes: the buffer holds
  already-redacted events, so unredacted PHI has no resting place in memory.
  Dropped events increment a `withheldEventCount` the caller can read. Also holds
  a bounded deque of `MediaRecorder` video chunks with a window of **120 seconds**,
  releasing chunks past the window.

**Tests:** a failing repository write leaves the local value in place, marks the
field dirty, and exposes `persistError` (assert it does **not** roll back);
eviction by count and by bytes independently and together, the
no-notify-on-equal path, subscription teardown, ingest-time redaction (assert a
raw PHI string is never observable in the buffer), video-chunk window release.

---

## Task 7: capture-core — deterministic step generator

Spec §8.1. The floor: every capture has usable steps with no LLM, no network.

**Deliverables** in `packages/capture-core/src/steps/`:

- `naming.ts` — `targetName(el)` resolution in this exact priority order:
  accessible name → associated label → trimmed text content → `data-testid` →
  CSS selector. Operates on a plain descriptor object, not a live DOM node, so
  `capture-core` stays framework- and DOM-free.
- `noise.ts` — scroll bursts coalesce (consecutive `scroll` events within **500 ms**
  collapse to one), `mousemove` is dropped entirely, repeated identical clicks
  (same selector, within **1000 ms**) fold into "clicked N times".
- `generate.ts` — `generateDoc(events, opts): ReplicationDoc` with
  `generator: 'deterministic'`, `generatorModel: null`. Output shape for a click is
  exactly: `Clicked "Save changes" on /patients/123/edit`. Every step carries the
  `eventIds` it was derived from and a `tVideo` equal to the event `t` when a video
  asset exists, else `null`. `title` is derived from the first error-level console
  event or the last navigation; `summary` is a one-line mechanical description;
  `expected` and `actual` are `null` (only the LLM pass infers those).

**Tests:** golden timeline fixtures → expected step output, kept as files under
`test/fixtures/timelines/`. Cover each naming fallback level, each noise rule at
and past its threshold, and the empty-timeline case.

---

## Task 8: capture-core — capture pipeline and export

Wires Tasks 2–7 into the state machine of spec §6 and produces the exportable
artifacts.

**Deliverables**

- `packages/capture-core/src/pipeline/machine.ts` — the transition table:
  `recording → redacting → composing → ready`, with `failed` reachable from
  `redacting` and `composing`, and `expired` from `ready`. Every other transition
  throws. Each transition appends a `lifecycle` event to the timeline.
- `packages/capture-core/src/pipeline/run.ts` — `finalizeCapture({ capture, buffer, repo, docGenerator })`:
  persists the buffered events, generates the document, and lands in `ready`.
  **It does not re-apply the ruleset.** Task 6 redacts on ingest, so the buffer
  already holds redacted events — that is the whole point of paying the
  continuous-redaction cost, and re-running here would be both redundant and a
  second place for the two passes to disagree. Hence no `redactor` or `ruleset`
  parameter in the signature.
  `withheldEventCount` is read from the **ingest** drop count the buffer already
  accumulated, not recomputed.
  Distinguish the two failure kinds, because Task 3 defines them differently:
  a **dropped event** is a successful fail-closed outcome and leaves the capture
  on the path to `ready`; only a **fatal pipeline error** (persistence failure,
  document generation crash, a buffer that cannot be read) lands in `failed`,
  where the capture is **not viewable**.
- `packages/capture-core/src/pipeline/gate.ts` — `assertReady(capture)` /
  `isViewable(capture)`. **Every** export, share, and route path in this repository
  calls the gate. Export it from the package index so no client can bypass it by
  accident.
- `packages/capture-core/src/export/markdown.ts` — renders a `ReplicationDoc` to
  markdown: title, summary, numbered steps with their cited event references,
  expected/actual, environment table, and — when applicable — the
  `fidelity: 'degraded'` notice and `N events withheld by redaction policy`.
- `packages/capture-core/src/export/htr.ts` — `buildHtrBundle(capture, events, assets)`
  producing a zip-shaped `.htr` bundle containing `capture.json`, `timeline.json`,
  `document.md`, and the asset blobs. Use a minimal hand-rolled **stored (no
  compression) ZIP writer** — no new dependency. Both export functions call the
  gate first and throw on a non-`ready` capture.

**Tests:** every illegal transition throws; a buffer containing dropped events
still reaches `ready` with `withheldEventCount` matching the ingest count (drops
are not failures); a fatal pipeline error produces `failed` and
`isViewable === false`; `finalizeCapture` is asserted not to invoke a redactor
at all; export from every non-`ready` state throws; the markdown snapshot
includes the degraded notice and withheld count; the `.htr` bundle round-trips
through a standard unzip.

---

## Task 9: packages/llm — pluggable providers and hallucination control

Spec §8.2, §8.3 and ADR-007. `packages/llm` depends on `@htr/capture-core` types
only.

**Deliverables**

- `src/provider.ts` — one interface both transports satisfy, carrying the
  locality that `selectProvider` has to enforce:

```ts
type ProviderTarget = 'localhost' | 'native-messaging' | 'remote';

type LlmProvider = {
  name: string;
  target: ProviderTarget;
  complete(req: { prompt: string; timeoutMs: number }): Promise<string>;
};
```

  `target` is **derived by the factory, never accepted from configuration** — a
  caller that could label a remote gateway `'localhost'` would defeat the entire
  local-only guarantee. It is the one field the policy layer trusts.
- `src/http-provider.ts` — `createHttpProvider({ baseUrl, apiKey?, model, fetch? })`,
  posting an OpenAI-compatible chat-completions request. Injectable `fetch`.
  Derives `target` by parsing `baseUrl`: `'localhost'` only for a loopback host
  (`localhost`, `127.0.0.0/8`, `[::1]`), otherwise `'remote'`. A hostname that
  merely *looks* local (`localhost.example.com`, a name that resolves to
  loopback) is `'remote'` — matching on the parsed host, not on a substring.
- `src/native-messaging-provider.ts` — `createNativeMessagingProvider({ connect, hostName })`
  over `chrome.runtime.connectNative`. The `connect` function is injected so the
  package never touches `chrome` directly and stays testable in node. `target` is
  always `'native-messaging'`.
- `src/policy.ts` — `selectProvider(policy, providers)` implementing an ordered
  fallback chain. **A local-only workspace never falls back to a remote target**:
  when `policy.localOnly` is true, the chain is filtered to providers whose
  `target` is `'localhost'` or `'native-messaging'` **before** selection, so a
  remote provider is never reachable — not first, not as fallback. A
  local-provider failure ends the chain and returns no provider; it never
  crosses into a remote call. A provider whose `target` the policy layer cannot
  read is treated as `'remote'`.
- `src/enrich.ts` — `enrichDoc({ doc, events, provider })`:
  - Deterministic steps are kept **unconditionally**; enrichment only adds.
  - Every LLM-authored step must cite `eventIds` that **exist in the timeline**
    *and* be validated as describing what those events contain. Existence is
    necessary, not sufficient — validate the step text against the cited events'
    kind and salient payload fields.
  - A step failing either check is **dropped individually, not flagged**. There is
    no partial-failure threshold.
  - If **zero** LLM steps survive, the entire LLM pass is discarded and the
    deterministic document stands alone.
  - A surviving pass sets `generator: 'llm'` and `generatorModel` to the model
    string. A discarded pass leaves `generator: 'deterministic'`.
  - **Every provider failure is non-fatal.** The capture stays `ready` with
    deterministic steps.

**Tests:** fuzz provider responses with fabricated `eventIds` and assert every
fabricated step is dropped; a response citing real IDs but describing something
the events do not contain is also dropped; zero-survivors discards the pass;
provider timeout, network error, and malformed JSON are all non-fatal; the
local-only policy never selects or falls back to a remote provider, including
when a remote provider is first in the chain and every local one has failed;
`createHttpProvider` derives `target: 'remote'` for `localhost.example.com` and
for a public host, and `'localhost'` only for real loopback forms.

---

## Task 10: packages/trackers — GitHub and GitLab issue routing

Spec §12 and ADR-008. Two real implementations from day one so the abstraction is
validated rather than speculative. **No client secret ships in this code.**

**Deliverables**

- `src/provider.ts`:

```ts
type TrackerProvider = {
  id: 'github' | 'gitlab';
  authorize(): Promise<AuthResult>;
  createIssue(input: IssueInput): Promise<{ url: string; id: string }>;
};
```

  `IssueInput` carries the rendered replication document as the body, asset
  attachments, a deep link back to the capture, and structured labels derived from
  `projectId` and detected error signatures.
- `src/github.ts` — OAuth **device flow** (`POST /login/device/code`, poll
  `POST /login/oauth/access_token` with `grant_type=urn:ietf:params:oauth:grant-type:device_code`).
  Honour `interval` and `slow_down`; surface `authorization_pending` as "keep
  polling", `expired_token` and `access_denied` as terminal. No `client_secret`
  anywhere.
- `src/gitlab.ts` — OAuth **PKCE**: S256 `code_challenge`, `code_verifier`
  exchange, no client secret.
- `src/token-store.ts` — access tokens go to `chrome.storage.session`; refresh
  tokens are **encrypted at rest** in `chrome.storage.local` using WebCrypto
  AES-GCM. Both storages are injected as interfaces so the package is
  node-testable and holds no direct `chrome` reference.
  The key and nonce lifecycle is specified, not left to the implementer, because
  every one of these choices is a way to lose or leak a refresh token:
  - **Key material:** a non-extractable `CryptoKey` from
    `crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 })`, generated once
    per install. Non-extractable means a compromised content script cannot read
    it out even with storage access.
  - **Key persistence:** stored as a `CryptoKey` in `chrome.storage.local`
    (structured-cloneable, so it survives service-worker restarts without ever
    being serialized to raw bytes). The MV3 service worker is evicted constantly;
    an in-memory-only key would log the user out on every idle timeout.
  - **Nonce:** a fresh 96-bit `crypto.getRandomValues` IV **per encryption**,
    stored alongside the ciphertext. Never derived, never reused, never a
    counter — GCM nonce reuse under one key is a catastrophic failure, not a
    degraded one.
  - **Rotation:** on every successful refresh-token rotation the token is
    re-encrypted under a newly generated key and the old key is discarded. There
    is no key-history store to search.
  - **Decryption failure** (corrupt ciphertext, missing key after a partial wipe,
    GCM tag mismatch) discards the stored refresh token and forces
    re-authorization. It never falls back to plaintext and never retries with a
    different key.
- `src/labels.ts` — label derivation from `projectId` and error signatures.
- Routing must call `capture-core`'s ready gate before building a payload.

**Tests:** contract tests against **recorded** GitHub/GitLab API fixtures (checked
in as JSON, served through an injected `fetch`). Cover the full device-flow poll
sequence including `slow_down`, PKCE verifier/challenge derivation, and a
non-`ready` capture being refused. For the token store specifically: an
encryption round-trip; the key surviving a simulated service-worker restart;
two encryptions of the same plaintext producing different IVs and different
ciphertext; rotation discarding the previous key; and each decryption-failure
mode (corrupt ciphertext, absent key, tag mismatch) discarding the token and
forcing re-authorization rather than returning anything.

---

## Task 11: clients/extension — MV3 shell, orchestration, and ambient capture

Spec §4.2, §7.4. This task owns everything except the offscreen recorder
(Task 12).

**Deliverables** in `clients/extension/`:

- `manifest.json` — MV3, `minimum_chrome_version` set, permissions limited to what
  is used: `debugger`, `webRequest`, `storage`, `offscreen`, `activeTab`,
  `scripting`, `tabs`. `host_permissions` must **not** be `<all_urls>` — the origin
  allow-list is enforced by policy, and `storage.managed` carries the ruleset
  (`managed_schema.json` declaring the ruleset shape).
- `src/background/service-worker.ts` — capture lifecycle orchestration: start,
  stop, Instant Replay arming, offscreen-document lifecycle, ruleset load from
  `chrome.storage.managed` with a refresh timer, and the state machine calls into
  `capture-core`. Refuse to start a capture on an off-allow-list origin — no
  capture at all, not even a screenshot, not even metadata.
- `src/background/cdp.ts` — attach `chrome.debugger` and subscribe to `Network`,
  `Console`, and `Runtime` domains, mapping their events to `CaptureEvent`s.
- `src/background/fallback.ts` — on `chrome.debugger.onDetach` (the DevTools-opened
  case), fall back to `webRequest` plus console monkey-patching, stamp the capture
  `fidelity: 'degraded'`, and append a `lifecycle` event recording the handoff.
  The badge must be visible downstream — silently losing console logs while
  presenting a confident document is worse than not capturing.
- `src/content/interaction.ts` — content script building the interaction trail and
  the DOM-derived target descriptors Task 7's naming consumes. It sends
  descriptors, never live nodes.
- `src/content/blur-regions.ts` — resolves `video-blur` selectors to screen rects
  and re-samples bounding boxes so they track scroll and layout, posting them to
  the offscreen document each frame.
- Vite multi-entry build producing `dist/` with the manifest and every entry.

**Tests:** Vitest with the `chrome` API stubbed at the boundary (the extension code
takes its Chrome surface through a thin injected adapter). Cover: off-allow-list
refusal, CDP detach → degraded handoff, ruleset refresh, and the interaction
descriptor mapping. Playwright E2E is **out of scope for this task** — noted for a
follow-up, not built here.

---

## Task 12: clients/extension — offscreen recorder with pre-encode blur

Invariant 2 lives here. Spec §7.2.

**Deliverables** in `clients/extension/src/offscreen/`:

- `recorder.ts` — receives the display stream, draws each frame to an
  `OffscreenCanvas`, composites blur rectangles (from Task 11's re-sampled
  bounding boxes) on top, and feeds **the canvas stream** to `MediaRecorder`.
  The unblurred display stream is never passed to `MediaRecorder` and never
  written anywhere.
- `blur.ts` — the compositing primitive: a box/stack blur over the given rects,
  drawn every frame. A rect list that fails to resolve blurs nothing **and stops
  the recording** rather than emitting an unblurred frame.
- `frame-budget.ts` — measures per-frame compositing cost over a rolling window.
  When the compositor misses its frame budget consistently (define and document
  the exact threshold: more than **3 consecutive frames** over **16.7 ms**, or a
  rolling mean above budget across **30 frames**), the capture
  **degrades to screenshot-only** — stop the video recorder, keep periodic
  screenshots, append a `lifecycle` event, and mark the capture
  `fidelity: 'degraded'`. Never emit unblurred video.
- `timeslice.ts` — `MediaRecorder` in timeslice mode writing fixed-duration chunks
  into the Task 6 bounded deque.

**Tests:** `jsdom` plus stubs for `OffscreenCanvas`, `MediaRecorder`, and
`requestAnimationFrame`. Assert: `MediaRecorder` is constructed with the **canvas**
stream and never the display stream; a blur-resolution failure stops recording;
each frame-budget threshold triggers the screenshot-only degradation exactly once;
the degradation path never re-enables video.

---

## Task 13: clients/viewer — playback, step linking, and the ⌘K palette

Spec §16. React 19 + Vite, reading the local observable store from Task 6.

**Deliverables** in `clients/viewer/`:

- `src/App.tsx` and routes: capture list, capture detail.
- `src/components/Timeline.tsx` — the ordered event timeline, one source, filtered
  by kind.
- `src/components/Player.tsx` — video playback with scrubbing driven by `tVideo`.
- `src/components/Steps.tsx` — each step clickable: scrubs the video to `tVideo`
  and highlights the cited events. Steps with no `tVideo` are still rendered.
- `src/components/FidelityBadge.tsx` — renders the `fidelity: 'degraded'` badge
  **prominently**, and `withheldEventCount` as
  `N events withheld by redaction policy`.
- `src/components/CommandPalette.tsx` — `⌘K` searching the local store only:
  captures, projects, actions, navigation. **No network request** on this path.
- `src/store/bindings.ts` — React bindings over the Task 6 observables via
  `useSyncExternalStore`, subscribing per field so a delta re-renders one cell.
- Styling: inline critical CSS in `index.html`, a boot script restoring
  last-known shell tokens (sidebar width, dark mode) from `localStorage` before
  first paint, `rel=modulepreload` with matching `crossorigin` on critical chunks,
  `target: 'esnext'` and one vendor chunk per npm package in the Vite config.
- Motion: **only `transform` and `opacity`**. Durations 100 ms quick, 250 ms
  regular, 350 ms ceiling; asymmetric — appear instantly, fade out over 150 ms.
- A capture that is not `ready` is not rendered as viewable anywhere in this app.

**Tests:** Vitest + Testing Library (`jsdom`). Cover the non-ready capture never
rendering its content, per-field subscription granularity, palette results coming
from the store with `fetch` asserted unused, step→scrub wiring, and the badge
rendering. A lint rule or test must fail on an animated `width`/`height`/`top`/
`left`/`margin` property.

---

## Task 14: clients/recording-link — no-login capture, export-only

Spec §13 Phase 0 and ADR-013. **Export-only. No upload. No server of ours.**

**Deliverables** in `clients/recording-link/`:

- `src/main.ts` — a standalone Vite page that captures via `getDisplayMedia`,
  runs the **same** `capture-core` redaction ruleset and pipeline, and produces a
  downloadable `.htr` bundle (video + timeline JSON + rendered markdown) via
  Task 8's builder.
- Ruleset delivery here cannot come from enterprise policy (no extension), so it
  is bundled at build time and its version is recorded on the capture. Document
  that difference in the file header.
- The download is the only egress. There is no fetch to any host we would operate.
- The page must state plainly what is captured and that nothing is transmitted.
- Blur still happens pre-encode using Task 12's `blur.ts` primitives — extract the
  shared compositing code into `capture-core` **only if** it needs no
  extension-specific API; otherwise duplicate deliberately and note why.

**Tests:** `jsdom` with `getDisplayMedia` stubbed. Assert the produced bundle is
built from a `ready` capture, that redaction ran with the bundled ruleset, and
that no `fetch` to a non-tracker host occurs on the capture path.

---

## Out of scope for this plan

Do not build any of these; they are later phases in spec §19:

- `services/*` (Go), sync engine, mutation queue, LWW, share links, SSO, RBAC
- `redaction-audit`, `summarizer`, `router`, `mcp-gateway`
- MCP server, webhooks, `cli/`
- iOS ReplayKit, Slack integration, JS SDK
- LRU eviction of synced captures (Phase 1 only — Phase 0 evicts nothing)
- Playwright extension E2E and the nightly OCR video-blur job (follow-up work)
