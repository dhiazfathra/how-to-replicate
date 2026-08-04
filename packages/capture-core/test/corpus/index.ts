import type { CaptureEvent } from '../../src/types/event.js';
import { PHI_PATTERNS } from '../../src/redaction/patterns.js';
import { parseRuleset, type RedactionRuleset } from '../../src/redaction/ruleset.js';
import { truncateBody, MAX_BODY_BYTES } from '../../src/redaction/truncate.js';

/**
 * The ruleset a real deployment would configure on top of the built-in PHI
 * patterns: a field-path rule for a known patient-name field (names have no
 * generic regex — they're redacted by schema position) and a dom-selector
 * rule for a known patient-name form field. Security/Compliance owns this
 * content per spec §18; this is the corpus's stand-in for it.
 */
export const STANDARD_RULESET: RedactionRuleset = parseRuleset({
  version: '1',
  rules: [
    ...PHI_PATTERNS,
    { id: 'fp:patient-name', class: 'field-path', pointer: '/patient/name' },
    { id: 'ds:patient-name', class: 'dom-selector', selector: '#patient-name' },
  ],
});

export type CorpusFixture = {
  /** Unique, human-readable id — shown in test failure output. */
  id: string;
  event: CaptureEvent;
  /** Synthetic PHI literals this fixture introduces; must never leak. */
  forbidden: string[];
  /**
   * Set to false only when the fixture's PHI never reaches the redactor at
   * all (e.g. dropped upstream by content-type policy) — such a fixture is
   * correctly `fidelity: 'full'` because there is nothing left to redact.
   */
  expectRedacted?: boolean;
  /** For the key-collision fixture: assert non-destructive key rewriting. */
  keyCountCheck?: { field: 'requestBody' | 'responseBody'; original: unknown };
};

function baseEvent(id: string, kind: CaptureEvent['kind'], payload: CaptureEvent['payload']): CaptureEvent {
  return {
    id,
    captureId: 'cap-corpus',
    t: 0,
    kind,
    payload,
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function toUnicodeEscaped(value: string): string {
  return [...value].map((ch) => `\\u${ch.charCodeAt(0).toString(16).padStart(4, '0')}`).join('');
}

// Synthetic PHI literals — never real data.
const NIK_NESTED = '3175010203040001';
const DOB_ISO = '1990-05-14';
const PHONE_NESTED = '081234567890';
const EMAIL_NESTED = 'siti.rahayu@example.com';
const PATIENT_NAME_1 = 'Siti Rahayu';
const BPJS_RESPONSE = '0219876543210';
const MRN_RESPONSE = 'MRN-2024007';
const MRN_HEADER = 'MRN-2024099';
const PHONE_HEADER = '081298765432';
const NIK_QUERY = '3175010203040002';
const NIK_NAV = '3175010203040003';
const PHONE_NAV = '081234567891';
const EMAIL_CONSOLE = 'siti.rahayu2@example.com';
const PHONE_STACK = '081234567892';
const PATIENT_NAME_2 = 'Siti Rahayu Kedua';
const PHONE_INTERACTION = '081234567893';
const NIK_ANNOTATION = '3175010203040004';
const EMAIL_ANNOTATION = 'siti.rahayu3@example.com';
const NIK_TRUNCATED = '3175010203040099';
const PHONE_NONJSON = '081234567894';
const NIK_NONJSON = '3175010203040005';
const PHONE_DROPPED_CONTENT_TYPE = '081234567999';
const PHONE_KEY = '081234567895';
const NIK_COLLIDE_A = '3175010203040006';
const NIK_COLLIDE_B = '3175010203040007';
const PHONE_UNICODE = '081234567896';
const NIK_FRAGMENT = '3175010203040008';
const EMAIL_FRAGMENT = 'siti.rahayu4@example.com';

const requestBodyNested = JSON.stringify({
  patient: { name: PATIENT_NAME_1, nik: NIK_NESTED, dob: DOB_ISO },
  visits: [
    { doctor: 'Dr. Andi', notes: `Patient contact ${PHONE_NESTED}` },
    { doctor: 'Dr. Budi', notes: { email: EMAIL_NESTED } },
  ],
});

const responseBodyJson = JSON.stringify({ status: 'ok', patient: { bpjs: BPJS_RESPONSE, mrn: MRN_RESPONSE } });

const collideOriginal = { [NIK_COLLIDE_A]: 'a', [NIK_COLLIDE_B]: 'b' };
const requestBodyCollide = JSON.stringify(collideOriginal);

function networkPayloadDefaults(): CaptureEvent['payload'] & {
  method: string;
  url: string;
  status: number | null;
  requestHeaders: Record<string, string>;
  responseHeaders: Record<string, string>;
  requestBody: string | null;
  responseBody: string | null;
  bodyTruncated: boolean;
  bodyDropped: boolean;
  durationMs: number | null;
  sizeBytes: number | null;
} {
  return {
    method: 'POST',
    url: 'https://api.example.com/patients',
    status: 200,
    requestHeaders: {},
    responseHeaders: {},
    requestBody: null,
    responseBody: null,
    bodyTruncated: false,
    bodyDropped: false,
    durationMs: 10,
    sizeBytes: 100,
  };
}

// --- truncation-boundary fixture: NIK straddles the 32KB cutoff -----------

const truncationFiller = 'x'.repeat(MAX_BODY_BYTES - 8);
const rawTruncationBody = JSON.stringify({ note: `${truncationFiller}${NIK_TRUNCATED} end` });
const truncated = truncateBody(rawTruncationBody, 'application/json');

// --- content-type-not-allowed fixture: dropped before the redactor sees it -

const rawDroppedBody = `contact ${PHONE_DROPPED_CONTENT_TYPE}`;
const droppedByContentType = truncateBody(rawDroppedBody, 'application/octet-stream');

// --- unicode-escaped PHI fixture -------------------------------------------

const requestBodyUnicode = `{"note":"${toUnicodeEscaped(PHONE_UNICODE)}"}`;

export const FIXTURES: CorpusFixture[] = [
  {
    id: 'json-body-nested-arrayed',
    event: baseEvent('evt-json-nested', 'network', {
      ...networkPayloadDefaults(),
      requestBody: requestBodyNested,
      responseBody: responseBodyJson,
    }),
    forbidden: [PATIENT_NAME_1, NIK_NESTED, DOB_ISO, PHONE_NESTED, EMAIL_NESTED, BPJS_RESPONSE, MRN_RESPONSE],
  },
  {
    id: 'request-and-response-headers',
    event: baseEvent('evt-headers', 'network', {
      ...networkPayloadDefaults(),
      requestHeaders: { 'X-Patient-MRN': MRN_HEADER },
      responseHeaders: { 'X-Patient-Phone': PHONE_HEADER },
    }),
    forbidden: [MRN_HEADER, PHONE_HEADER],
  },
  {
    id: 'url-and-query-string',
    event: baseEvent('evt-url', 'network', {
      ...networkPayloadDefaults(),
      url: `https://api.example.com/patients/search?nik=${NIK_QUERY}`,
    }),
    forbidden: [NIK_QUERY],
  },
  {
    id: 'navigation-url',
    event: baseEvent('evt-nav', 'navigation', {
      from: `https://app.example.com/patients/${NIK_NAV}`,
      to: `https://app.example.com/patients/${NIK_NAV}/edit?phone=${PHONE_NAV}`,
      trigger: 'pushstate',
    }),
    forbidden: [NIK_NAV, PHONE_NAV],
  },
  {
    id: 'console-text-and-stack',
    event: baseEvent('evt-console', 'console', {
      level: 'error',
      text: `Failed to load record for patient email ${EMAIL_CONSOLE}`,
      stack: `Error: fetch failed\n at handler (patient.js:12)\n contact ${PHONE_STACK} for support`,
    }),
    forbidden: [EMAIL_CONSOLE, PHONE_STACK],
  },
  {
    id: 'interaction-target-name-dom-selector',
    event: baseEvent('evt-interaction-name', 'interaction', {
      type: 'input',
      targetName: `Nama Pasien: ${PATIENT_NAME_2}`,
      targetSelector: '#patient-name',
      url: 'https://app.example.com/intake',
      value: PATIENT_NAME_2,
    }),
    forbidden: [PATIENT_NAME_2],
  },
  {
    id: 'interaction-value-pattern',
    event: baseEvent('evt-interaction-phone', 'interaction', {
      type: 'input',
      targetName: 'Phone',
      targetSelector: '#phone-input',
      url: 'https://app.example.com/intake',
      value: PHONE_INTERACTION,
    }),
    forbidden: [PHONE_INTERACTION],
  },
  {
    id: 'annotation-text',
    event: baseEvent('evt-annotation', 'annotation', {
      text: `Bug reproduced after entering NIK ${NIK_ANNOTATION} and email ${EMAIL_ANNOTATION}`,
    }),
    forbidden: [NIK_ANNOTATION, EMAIL_ANNOTATION],
  },
  {
    id: 'adversarial-truncation-boundary-split',
    event: baseEvent('evt-adv-truncate', 'network', {
      ...networkPayloadDefaults(),
      requestBody: truncated.body,
      bodyTruncated: truncated.bodyTruncated,
      bodyDropped: truncated.bodyDropped,
    }),
    forbidden: [NIK_TRUNCATED],
    // Truncation lands mid-NIK, so no rule has anything left to touch — the
    // full literal never reaches the redactor in one piece. `fidelity: 'full'`
    // here means the truncation layer already did its job, not a leak.
    expectRedacted: false,
  },
  {
    id: 'adversarial-non-json-body',
    event: baseEvent('evt-adv-nonjson', 'network', {
      ...networkPayloadDefaults(),
      requestBody: `Contact patient at ${PHONE_NONJSON} re NIK ${NIK_NONJSON}`,
    }),
    forbidden: [PHONE_NONJSON, NIK_NONJSON],
  },
  {
    id: 'adversarial-disallowed-content-type',
    event: baseEvent('evt-adv-contenttype', 'network', {
      ...networkPayloadDefaults(),
      requestBody: droppedByContentType.body,
      bodyDropped: droppedByContentType.bodyDropped,
      bodyTruncated: droppedByContentType.bodyTruncated,
    }),
    forbidden: [PHONE_DROPPED_CONTENT_TYPE],
    expectRedacted: false,
  },
  {
    id: 'adversarial-phi-in-key',
    event: baseEvent('evt-adv-key', 'network', {
      ...networkPayloadDefaults(),
      requestBody: JSON.stringify({ [PHONE_KEY]: 'appointment reminder' }),
    }),
    forbidden: [PHONE_KEY],
  },
  {
    id: 'adversarial-colliding-sibling-keys',
    event: baseEvent('evt-adv-collide', 'network', {
      ...networkPayloadDefaults(),
      requestBody: requestBodyCollide,
    }),
    forbidden: [NIK_COLLIDE_A, NIK_COLLIDE_B],
    keyCountCheck: { field: 'requestBody', original: collideOriginal },
  },
  {
    id: 'adversarial-unicode-escaped-phi',
    event: baseEvent('evt-adv-unicode', 'network', {
      ...networkPayloadDefaults(),
      requestBody: requestBodyUnicode,
    }),
    forbidden: [PHONE_UNICODE],
  },
  {
    id: 'adversarial-url-fragment',
    event: baseEvent('evt-adv-fragment', 'network', {
      ...networkPayloadDefaults(),
      url: `https://app.example.com/patients/view?tab=summary#nik=${NIK_FRAGMENT};email=${EMAIL_FRAGMENT}`,
    }),
    forbidden: [NIK_FRAGMENT, EMAIL_FRAGMENT],
  },
];

/** Every synthetic PHI literal used anywhere in the corpus. Never real data. */
export const FORBIDDEN_STRINGS: string[] = [...new Set(FIXTURES.flatMap((fixture) => fixture.forbidden))];
