/**
 * In-page e2e harness. Runs inside the real browser tab on the same origin
 * as the viewer, so everything it persists lands in the very IndexedDB the
 * viewer opens.
 *
 * It plays exactly the part the MV3 service worker plays in production —
 * owning the clock, the `InstantReplay` buffer and the capture state machine
 * — and nothing else. Every piece of behavior under test is imported product
 * code:
 *
 *   - redaction:    `createRedactor` / `PHI_PATTERNS`   (capture-core)
 *   - buffering:    `createInstantReplay` / ring buffer (capture-core)
 *   - CDP mapping:  `mapCdpEvent`                       (clients/extension)
 *   - interactions: `startInteractionTrail`             (clients/extension)
 *   - finalize:     `finalizeCapture` / `transition`    (capture-core)
 *   - persistence:  `openCaptureDb` / `CaptureRepository` (capture-core)
 *
 * The harness holds no redaction, step-generation or persistence logic of its
 * own. The one thing it mints itself is the `navigation` event (see
 * `trackNavigation`) — no production module emits those yet.
 */
import {
  CaptureRepository,
  PHI_PATTERNS,
  createClock,
  createInstantReplay,
  createRedactor,
  createRingBuffer,
  finalizeCapture,
  newId,
  openIdentityPartitionDb,
  parseRuleset,
  transition,
  type Capture,
  type CaptureEvent,
  type Clock,
  type Identity,
  type InstantReplay,
  type NavigationPayload,
  type RedactionRuleset,
} from '@htr/capture-core';
import { mapCdpEvent } from '../../clients/extension/src/background/cdp.js';
import { startInteractionTrail } from '../../clients/extension/src/content/interaction.js';
import type { Harness } from './api.js';

export const E2E_RULESET_VERSION = '2026-08-05.e2e.1';

/**
 * The policy this harness enforces. Deliberately built the way a real
 * enterprise ruleset is: the origin allow-list, the credential header, one
 * `field-path` rule aimed at the API's documented body shape, and
 * capture-core's own built-in synthetic-PHI pattern library.
 *
 * The `field-path` rule is what makes invariant 4 observable: the demo app
 * has a legacy endpoint that sends `patient` as a bare string instead of an
 * object, so the pointer lands on a scalar, the rule throws, and the engine
 * fails closed by dropping the whole event.
 */
function buildRuleset(): RedactionRuleset {
  return parseRuleset({
    version: E2E_RULESET_VERSION,
    rules: [
      { id: 'origin-allow:e2e', class: 'origin-allow', origins: [window.location.origin] },
      { id: 'header:authorization', class: 'header', name: 'authorization' },
      { id: 'field-path:patient-mrn', class: 'field-path', pointer: '/patient/mrn' },
      ...PHI_PATTERNS,
      { id: 'video-blur:patient-banner', class: 'video-blur', selector: '#patient-banner' },
    ],
  });
}

const ruleset = buildRuleset();

// The viewer only ever reads the partition of the last-authenticated
// identity (`bootstrapSession`) — an anonymous, unpartitioned database is
// invisible to it by design. This harness stands in for a real logged-in
// session by recording the same fixed identity a real `login()` call would,
// so everything it seeds or persists lands where the app actually looks.
const E2E_IDENTITY: Identity = { subject: 'e2e-test-user', workspaceId: 'e2e-test-workspace' };

let dbPromise: ReturnType<typeof openIdentityPartitionDb> | null = null;
function getDb(): ReturnType<typeof openIdentityPartitionDb> {
  dbPromise ??= openIdentityPartitionDb(E2E_IDENTITY);
  return dbPromise;
}

async function getRepo(): Promise<CaptureRepository> {
  return new CaptureRepository(await getDb());
}

function envSnapshot(): Capture['env'] {
  return {
    userAgent: navigator.userAgent,
    platform: navigator.platform,
    viewport: { w: window.innerWidth, h: window.innerHeight },
    devicePixelRatio: window.devicePixelRatio,
    locale: navigator.language,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    url: window.location.href,
  };
}

function newCapture(clock: Clock): Capture {
  return {
    id: newId(),
    workspaceId: null,
    projectId: 'clinic-portal',
    source: 'extension',
    state: 'recording',
    fidelity: 'full',
    createdAt: new Date().toISOString(),
    epoch: clock.epoch,
    env: envSnapshot(),
    metadata: { rulesetVersion: ruleset.version },
    doc: null,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
  };
}

/**
 * Mint a `navigation` event from a real `hashchange`. This is the only event
 * kind the harness builds itself: `mapCdpEvent` maps Network/Console/Runtime
 * only, and the content script's trail covers interactions, so nothing in
 * `clients/` currently emits `navigation` even though `generateDoc` renders
 * it. Called out in the e2e README as a real coverage gap, not papered over.
 */
function trackNavigation(
  captureId: string,
  clock: Clock,
  emit: (event: CaptureEvent) => void,
): () => void {
  let from = window.location.href;
  const handler = (): void => {
    const to = window.location.href;
    const payload: NavigationPayload = { from, to, trigger: 'hashchange' };
    from = to;
    emit({
      id: newId(),
      captureId,
      t: clock.now(),
      kind: 'navigation',
      payload,
      redaction: { rulesApplied: [], fidelity: 'full' },
    });
  };
  window.addEventListener('hashchange', handler);
  return () => {
    window.removeEventListener('hashchange', handler);
  };
}

type Session = {
  capture: Capture;
  clock: Clock;
  buffer: InstantReplay;
  pending: Parameters<typeof mapCdpEvent>[3];
  teardown: () => void;
};

let session: Session | null = null;

function requireSession(): Session {
  if (!session) throw new Error('__htr: no capture in progress — call start() first');
  return session;
}

const harness: Harness = {
  ruleset: () => ruleset,

  start(): string {
    if (session) throw new Error('__htr: a capture is already in progress');
    const clock = createClock();
    const capture = newCapture(clock);
    const buffer = createInstantReplay({
      redactor: createRedactor(ruleset),
      ring: createRingBuffer<CaptureEvent>(),
      clock,
    });
    const emit = (event: CaptureEvent): void => {
      buffer.ingest(event);
    };

    const stopTrail = startInteractionTrail(
      document,
      capture.id,
      clock,
      () => window.location.href,
      emit,
    );
    const stopNav = trackNavigation(capture.id, clock, emit);

    session = {
      capture,
      clock,
      buffer,
      pending: new Map(),
      teardown: () => {
        stopTrail();
        stopNav();
      },
    };
    return capture.id;
  },

  pushCdp(message): void {
    const current = session;
    if (!current) return; // CDP traffic before start()/after stop() is not part of any capture.
    const mapped = mapCdpEvent(message, current.capture.id, current.clock, current.pending);
    if (mapped) current.buffer.ingest(mapped);
  },

  events() {
    return requireSession().buffer.events();
  },

  stats() {
    const current = requireSession();
    return {
      events: current.buffer.events().length,
      withheld: current.buffer.withheldEventCount(),
    };
  },

  async stop(): Promise<Capture> {
    const current = requireSession();
    current.teardown();
    session = null;

    // Same walk the extension's `serviceWorker.stop()` performs.
    const { capture: redacting, event: redactingEvent } = transition(
      current.capture,
      'redacting',
      null,
      current.clock.now(),
    );
    current.buffer.ingest(redactingEvent);
    const { capture: composing, event: composingEvent } = transition(
      redacting,
      'composing',
      null,
      current.clock.now(),
    );
    current.buffer.ingest(composingEvent);

    return finalizeCapture({
      capture: composing,
      buffer: current.buffer,
      repo: await getRepo(),
      clock: current.clock,
    });
  },

  async seedCapture(overrides): Promise<Capture> {
    const capture: Capture = { ...newCapture(createClock()), ...overrides };
    await (await getRepo()).putCapture(capture);
    return capture;
  },

  async listCaptures() {
    return (await getRepo()).listCaptures();
  },

  async readEvents(captureId) {
    return (await getRepo()).readEvents(captureId);
  },
};

window.__htr = harness;
