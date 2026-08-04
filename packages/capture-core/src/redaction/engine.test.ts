import { describe, expect, it } from 'vitest';
import type {
  AnnotationPayload,
  CaptureEvent,
  ConsolePayload,
  InteractionPayload,
  LifecyclePayload,
  NavigationPayload,
  NetworkPayload,
} from '../types/event.js';
import { createRedactor } from './engine.js';
import { parseRuleset, type RedactionRuleset } from './ruleset.js';

function networkEvent(payload: Partial<NetworkPayload> = {}): CaptureEvent {
  return {
    id: 'evt-1',
    captureId: 'cap-1',
    t: 0,
    kind: 'network',
    payload: {
      method: 'GET',
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
      ...payload,
    },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function consoleEvent(payload: Partial<ConsolePayload> = {}): CaptureEvent {
  return {
    id: 'evt-2',
    captureId: 'cap-1',
    t: 0,
    kind: 'console',
    payload: { level: 'log', text: 'hello', stack: null, ...payload },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function interactionEvent(payload: Partial<InteractionPayload> = {}): CaptureEvent {
  return {
    id: 'evt-3',
    captureId: 'cap-1',
    t: 0,
    kind: 'interaction',
    payload: {
      type: 'input',
      targetName: 'Full name',
      targetSelector: '#name',
      url: 'https://example.com',
      value: 'Jane Doe',
      ...payload,
    },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function annotationEvent(payload: Partial<AnnotationPayload> = {}): CaptureEvent {
  return {
    id: 'evt-4',
    captureId: 'cap-1',
    t: 0,
    kind: 'annotation',
    payload: { text: 'note', ...payload },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function navigationEvent(payload: Partial<NavigationPayload> = {}): CaptureEvent {
  return {
    id: 'evt-5',
    captureId: 'cap-1',
    t: 0,
    kind: 'navigation',
    payload: { from: null, to: 'https://example.com', trigger: 'load', ...payload },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function lifecycleEvent(payload: Partial<LifecyclePayload> = {}): CaptureEvent {
  return {
    id: 'evt-6',
    captureId: 'cap-1',
    t: 0,
    kind: 'lifecycle',
    payload: { transition: 'start', detail: null, ...payload },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function ruleset(rules: unknown[]): RedactionRuleset {
  return parseRuleset({ version: '1', rules });
}

describe('createRedactor: field-path', () => {
  it('redacts a matched pointer in the request and response body', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'fp', class: 'field-path', pointer: '/patient/name' }]),
    );
    const event = networkEvent({
      requestBody: JSON.stringify({ patient: { name: 'Jane', age: 30 } }),
      responseBody: JSON.stringify({ patient: { name: 'Jane' } }),
    });
    const outcome = redactor.redactEvent(event);
    expect(outcome.fidelity).toBe('redacted');
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    const payload = outcome.event.payload as NetworkPayload;
    expect(JSON.parse(payload.requestBody ?? '')).toEqual({ patient: { name: '[REDACTED]', age: 30 } });
    expect(JSON.parse(payload.responseBody ?? '')).toEqual({ patient: { name: '[REDACTED]' } });
    expect(outcome.event.redaction.rulesApplied).toEqual(['fp']);
  });

  it('supports a wildcard segment matching every child at that level', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/patients/*/name' }]));
    const event = networkEvent({
      requestBody: JSON.stringify({ patients: [{ name: 'A' }, { name: 'B' }] }),
    });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    const payload = outcome.event.payload as NetworkPayload;
    expect(JSON.parse(payload.requestBody ?? '')).toEqual({
      patients: [{ name: '[REDACTED]' }, { name: '[REDACTED]' }],
    });
  });

  it('supports a wildcard segment over object children', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/patients/*/name' }]));
    const event = networkEvent({
      requestBody: JSON.stringify({ patients: { a: { name: 'A' }, b: { name: 'B' } } }),
    });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    const payload = outcome.event.payload as NetworkPayload;
    expect(JSON.parse(payload.requestBody ?? '')).toEqual({
      patients: { a: { name: '[REDACTED]' }, b: { name: '[REDACTED]' } },
    });
  });

  it('does not apply to a non-network event', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/name' }]));
    const outcome = redactor.redactEvent(consoleEvent());
    expect(outcome.fidelity).toBe('full');
  });

  it('falls through to leave a non-JSON body untouched (not exempt, but not this rule)', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/name' }]));
    const event = networkEvent({ requestBody: 'not json' });
    const outcome = redactor.redactEvent(event);
    expect(outcome.fidelity).toBe('full');
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect((outcome.event.payload as NetworkPayload).requestBody).toBe('not json');
  });

  it('is a no-op when the pointer path does not exist', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/missing/path' }]));
    const event = networkEvent({ requestBody: JSON.stringify({ patient: { name: 'A' } }) });
    const outcome = redactor.redactEvent(event);
    expect(outcome.fidelity).toBe('full');
  });

  it('redacts a specific array index by numeric segment', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/items/1' }]));
    const event = networkEvent({ requestBody: JSON.stringify({ items: ['a', 'b', 'c'] }) });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect(JSON.parse((outcome.event.payload as NetworkPayload).requestBody ?? '')).toEqual({
      items: ['a', '[REDACTED]', 'c'],
    });
  });

  it('is a no-op when an array pointer index is out of range', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/items/5' }]));
    const event = networkEvent({ requestBody: JSON.stringify({ items: ['a'] }) });
    const outcome = redactor.redactEvent(event);
    expect(outcome.fidelity).toBe('full');
  });

  it('redacts a whole body with the empty pointer', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '' }]));
    const event = networkEvent({ requestBody: JSON.stringify({ a: 1 }) });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect((outcome.event.payload as NetworkPayload).requestBody).toBe(JSON.stringify('[REDACTED]'));
  });

  it('drops the event when a pointer segment expects a container but finds a primitive', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/name/first' }]));
    const event = networkEvent({ requestBody: JSON.stringify({ name: 'Jane' }) });
    const outcome = redactor.redactEvent(event);
    expect(outcome.fidelity).toBe('dropped');
    if (outcome.fidelity !== 'dropped') throw new Error('expected drop');
    expect(outcome.ruleId).toBe('fp');
    expect(outcome.reason).not.toContain('Jane');
  });

  it('drops the event when a wildcard segment lands on a primitive', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/name/*' }]));
    const event = networkEvent({ requestBody: JSON.stringify({ name: 'Jane' }) });
    const outcome = redactor.redactEvent(event);
    expect(outcome.fidelity).toBe('dropped');
  });

  it('accepts a pointer without a leading slash', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: 'name' }]));
    const event = networkEvent({ requestBody: JSON.stringify({ name: 'Jane' }) });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect(JSON.parse((outcome.event.payload as NetworkPayload).requestBody ?? '')).toEqual({
      name: '[REDACTED]',
    });
  });

  it('supports RFC 6901 escaped segments', () => {
    const redactor = createRedactor(ruleset([{ id: 'fp', class: 'field-path', pointer: '/a~1b' }]));
    const event = networkEvent({ requestBody: JSON.stringify({ 'a/b': 'secret' }) });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect(JSON.parse((outcome.event.payload as NetworkPayload).requestBody ?? '')).toEqual({
      'a/b': '[REDACTED]',
    });
  });
});

describe('createRedactor: header', () => {
  it('redacts a matching header case-insensitively on request and response', () => {
    const redactor = createRedactor(ruleset([{ id: 'h', class: 'header', name: 'authorization' }]));
    const event = networkEvent({
      requestHeaders: { Authorization: 'Bearer secret' },
      responseHeaders: { AUTHORIZATION: 'token' },
    });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    const payload = outcome.event.payload as NetworkPayload;
    expect(payload.requestHeaders.Authorization).toBe('[REDACTED]');
    expect(payload.responseHeaders.AUTHORIZATION).toBe('[REDACTED]');
    expect(outcome.event.redaction.rulesApplied).toEqual(['h']);
  });

  it('is a no-op when no header matches', () => {
    const redactor = createRedactor(ruleset([{ id: 'h', class: 'header', name: 'x-secret' }]));
    const event = networkEvent({ requestHeaders: { 'content-type': 'application/json' } });
    const outcome = redactor.redactEvent(event);
    expect(outcome.fidelity).toBe('full');
  });

  it('does not apply to non-network events', () => {
    const redactor = createRedactor(ruleset([{ id: 'h', class: 'header', name: 'x' }]));
    const outcome = redactor.redactEvent(consoleEvent());
    expect(outcome.fidelity).toBe('full');
  });
});

describe('createRedactor: pattern', () => {
  it('redacts a matching value in a simple string payload field', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', label: 'digits' }]),
    );
    const outcome = redactor.redactEvent(consoleEvent({ text: 'code is 1234 ok' }));
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect((outcome.event.payload as ConsolePayload).text).toBe('code is [REDACTED:digits] ok');
  });

  it('applies across every payload kind that carries strings', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', label: 'digits' }]),
    );
    expect(
      (redactor.redactEvent(interactionEvent({ value: '1234' })) as { event: CaptureEvent }).event.payload,
    ).toMatchObject({ value: '[REDACTED:digits]' });
    expect(
      (redactor.redactEvent(annotationEvent({ text: 'id 1234' })) as { event: CaptureEvent }).event.payload,
    ).toMatchObject({ text: 'id [REDACTED:digits]' });
    expect(
      (redactor.redactEvent(navigationEvent({ to: 'https://x.com/1234' })) as { event: CaptureEvent }).event
        .payload,
    ).toMatchObject({ to: 'https://x.com/[REDACTED:digits]' });
    expect(
      (redactor.redactEvent(lifecycleEvent({ detail: '1234' })) as { event: CaptureEvent }).event.payload,
    ).toMatchObject({ detail: '[REDACTED:digits]' });
  });

  it('redacts matching header values via the generic string walk', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', label: 'digits' }]),
    );
    const outcome = redactor.redactEvent(networkEvent({ requestHeaders: { 'x-id': '1234' } }));
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect((outcome.event.payload as NetworkPayload).requestHeaders['x-id']).toBe('[REDACTED:digits]');
  });

  it('redacts matches inside a JSON body, including object keys', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', label: 'digits' }]),
    );
    const event = networkEvent({ requestBody: JSON.stringify({ '1234': 'value', other: '5678' }) });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    const body: unknown = JSON.parse((outcome.event.payload as NetworkPayload).requestBody ?? '');
    expect(body).toEqual({ '[REDACTED:digits]': 'value', other: '[REDACTED:digits]' });
  });

  it('suffixes colliding redacted keys by ordinal position instead of overwriting', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '08\\d{2}', label: 'phone-id' }]),
    );
    const event = networkEvent({ requestBody: JSON.stringify({ '0812': 1, '0813': 2 }) });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    const body: unknown = JSON.parse((outcome.event.payload as NetworkPayload).requestBody ?? '');
    expect(body).toEqual({ '[REDACTED:phone-id]': 1, '[REDACTED:phone-id]#2': 2 });
  });

  it('redacts matches inside an array reached through a JSON body', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', label: 'digits' }]),
    );
    const event = networkEvent({ requestBody: JSON.stringify({ codes: ['1234', 'ok'] }) });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    const body: unknown = JSON.parse((outcome.event.payload as NetworkPayload).requestBody ?? '');
    expect(body).toEqual({ codes: ['[REDACTED:digits]', 'ok'] });
  });

  it('scans a non-JSON body as plain text', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', label: 'digits' }]),
    );
    const event = networkEvent({ requestBody: 'plain text 9999 body' });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect((outcome.event.payload as NetworkPayload).requestBody).toBe('plain text [REDACTED:digits] body');
  });

  it('is a no-op when nothing matches', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', label: 'digits' }]),
    );
    const outcome = redactor.redactEvent(consoleEvent({ text: 'no digits here' }));
    expect(outcome.fidelity).toBe('full');
  });

  it('leaves a null stack/detail/value untouched', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', label: 'digits' }]),
    );
    const outcome = redactor.redactEvent(consoleEvent({ stack: null }));
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect((outcome.event.payload as ConsolePayload).stack).toBeNull();
  });

  it('does not double up the global flag when it is already present', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', flags: 'g', label: 'digits' }]),
    );
    const outcome = redactor.redactEvent(consoleEvent({ text: '1234 and 5678' }));
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect((outcome.event.payload as ConsolePayload).text).toBe('[REDACTED:digits] and [REDACTED:digits]');
  });

  it('respects an explicit flags string', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: 'secret', flags: 'i', label: 'kw' }]),
    );
    const outcome = redactor.redactEvent(consoleEvent({ text: 'SECRET value' }));
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect((outcome.event.payload as ConsolePayload).text).toBe('[REDACTED:kw] value');
  });
});

describe('createRedactor: dom-selector', () => {
  it('redacts targetName and value when targetSelector matches', () => {
    const redactor = createRedactor(ruleset([{ id: 'd', class: 'dom-selector', selector: '#name' }]));
    const outcome = redactor.redactEvent(interactionEvent({ targetSelector: '#name' }));
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    const payload = outcome.event.payload as InteractionPayload;
    expect(payload.targetName).toBe('[REDACTED]');
    expect(payload.value).toBe('[REDACTED]');
    expect(outcome.event.redaction.rulesApplied).toEqual(['d']);
  });

  it('keeps a null value null when the selector matches', () => {
    const redactor = createRedactor(ruleset([{ id: 'd', class: 'dom-selector', selector: '#name' }]));
    const outcome = redactor.redactEvent(interactionEvent({ targetSelector: '#name', value: null }));
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect((outcome.event.payload as InteractionPayload).value).toBeNull();
  });

  it('is a no-op when the selector does not match', () => {
    const redactor = createRedactor(ruleset([{ id: 'd', class: 'dom-selector', selector: '#other' }]));
    const outcome = redactor.redactEvent(interactionEvent({ targetSelector: '#name' }));
    expect(outcome.fidelity).toBe('full');
  });

  it('does not apply to non-interaction events', () => {
    const redactor = createRedactor(ruleset([{ id: 'd', class: 'dom-selector', selector: '#name' }]));
    const outcome = redactor.redactEvent(consoleEvent());
    expect(outcome.fidelity).toBe('full');
  });
});

describe('createRedactor: origin-allow', () => {
  it('allows only listed origins', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'o', class: 'origin-allow', origins: ['https://a.com'] }]),
    );
    expect(redactor.isOriginAllowed('https://a.com')).toBe(true);
    expect(redactor.isOriginAllowed('https://b.com')).toBe(false);
  });

  it('fails closed when there is no origin-allow rule at all', () => {
    const redactor = createRedactor(ruleset([]));
    expect(redactor.isOriginAllowed('https://anything.com')).toBe(false);
  });

  it('fails closed when the origin-allow rule lists no origins', () => {
    const redactor = createRedactor(ruleset([{ id: 'o', class: 'origin-allow', origins: [] }]));
    expect(redactor.isOriginAllowed('https://anything.com')).toBe(false);
  });

  it('never appears in rulesApplied since it does not touch event payloads', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'o', class: 'origin-allow', origins: ['https://a.com'] }]),
    );
    const outcome = redactor.redactEvent(consoleEvent());
    expect(outcome.fidelity).toBe('full');
  });
});

describe('createRedactor: video-blur', () => {
  it('exposes blur selectors without touching event payloads', () => {
    const redactor = createRedactor(
      ruleset([
        { id: 'v1', class: 'video-blur', selector: '.face' },
        { id: 'v2', class: 'video-blur', selector: '.ssn' },
      ]),
    );
    expect(redactor.blurSelectors()).toEqual(['.face', '.ssn']);
    const outcome = redactor.redactEvent(consoleEvent());
    expect(outcome.fidelity).toBe('full');
  });

  it('returns an empty list when there are no video-blur rules', () => {
    const redactor = createRedactor(ruleset([]));
    expect(redactor.blurSelectors()).toEqual([]);
  });
});

describe('createRedactor: rulesApplied ordering and dedup', () => {
  it('lists fired rule ids in ruleset order, deduplicated', () => {
    const redactor = createRedactor(
      ruleset([
        { id: 'p1', class: 'pattern', pattern: '99', label: 'x' },
        { id: 'h1', class: 'header', name: 'authorization' },
        { id: 'p2', class: 'pattern', pattern: '42', label: 'y' },
      ]),
    );
    const event = networkEvent({
      requestHeaders: { authorization: '99' },
      requestBody: JSON.stringify({ code: '42' }),
    });
    const outcome = redactor.redactEvent(event);
    if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
    expect(outcome.event.redaction.rulesApplied).toEqual(['p1', 'h1', 'p2']);
  });

  it('is pure: the same event and ruleset always produce the same outcome', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'p', class: 'pattern', pattern: '\\d{4}', label: 'digits' }]),
    );
    const event = consoleEvent({ text: 'value 1234' });
    const first = redactor.redactEvent(event);
    const second = redactor.redactEvent(event);
    expect(first).toEqual(second);
  });
});

describe('createRedactor: fail-closed error handling', () => {
  it('drops the event and attributes the failure to the offending rule', () => {
    const redactor = createRedactor(
      ruleset([{ id: 'bad-fp', class: 'field-path', pointer: '/name/first' }]),
    );
    const event = networkEvent({ requestBody: JSON.stringify({ name: 'Jane' }) });
    const outcome = redactor.redactEvent(event);
    expect(outcome).toMatchObject({ fidelity: 'dropped', ruleId: 'bad-fp' });
  });

  it('never throws and falls back to engine:internal-error for a failure outside any single rule', () => {
    const redactor = createRedactor(ruleset([]));
    const hostileEvent = consoleEvent();
    Object.defineProperty(hostileEvent, 'captureId', {
      enumerable: true,
      get(): string {
        throw new Error('cannot read captureId');
      },
    });

    let outcome: ReturnType<typeof redactor.redactEvent> | undefined;
    expect(() => {
      outcome = redactor.redactEvent(hostileEvent);
    }).not.toThrow();
    expect(outcome).toMatchObject({ fidelity: 'dropped', ruleId: 'engine:internal-error' });
  });

  it('never throws and falls back to engine:internal-error when cloning the event fails', () => {
    const redactor = createRedactor(ruleset([]));
    const hostileEvent = {
      ...consoleEvent(),
      payload: { level: 'log', text: 'hi', stack: null, poison: () => undefined },
    } as unknown as CaptureEvent;

    let outcome: ReturnType<typeof redactor.redactEvent> | undefined;
    expect(() => {
      outcome = redactor.redactEvent(hostileEvent);
    }).not.toThrow();
    expect(outcome).toMatchObject({ fidelity: 'dropped', ruleId: 'engine:internal-error' });
    if (outcome?.fidelity !== 'dropped') throw new Error('expected drop');
    expect(outcome.reason).not.toContain('hi');
  });
});
