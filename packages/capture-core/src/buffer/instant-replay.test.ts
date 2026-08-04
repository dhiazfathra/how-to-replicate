import { describe, expect, it } from 'vitest';
import { createInstantReplay, VIDEO_WINDOW_MS } from './instant-replay.js';
import { createRingBuffer } from './ring.js';
import { createRedactor } from '../redaction/engine.js';
import { parseRuleset } from '../redaction/ruleset.js';
import type { CaptureEvent, ConsolePayload } from '../types/event.js';

const PHI_EMAIL = 'jane.doe@example.com';

function consoleEvent(id: string, text: string): CaptureEvent {
  const payload: ConsolePayload = { level: 'log', text, stack: null };
  return {
    id,
    captureId: 'cap-1',
    t: 0,
    kind: 'console',
    payload,
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function emailRedactor() {
  return createRedactor(
    parseRuleset({
      version: '1',
      rules: [
        {
          id: 'builtin:email',
          class: 'pattern',
          pattern: '\\b[\\w.+-]+@[\\w-]+\\.[A-Za-z]{2,}\\b',
          flags: 'g',
          label: 'email',
        },
      ],
    }),
  );
}

function dropEvent(): CaptureEvent {
  // A field-path pointer whose next segment expects a container but finds a
  // primitive is a documented drop path in the redaction engine (see
  // src/redaction/engine.test.ts): pointer "/name/first" into { name: 'Jane' }.
  return {
    id: 'e-drop',
    captureId: 'cap-1',
    t: 0,
    kind: 'network',
    payload: {
      method: 'GET',
      url: 'https://api.example.com',
      status: 200,
      requestHeaders: {},
      responseHeaders: {},
      requestBody: JSON.stringify({ name: 'Jane' }),
      responseBody: null,
      bodyTruncated: false,
      bodyDropped: false,
      durationMs: 10,
      sizeBytes: 100,
    },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function dropAllRedactor() {
  return createRedactor(
    parseRuleset({
      version: '1',
      rules: [{ id: 'fp', class: 'field-path', pointer: '/name/first' }],
    }),
  );
}

function fixedClock(t = 0) {
  let now = t;
  return { now: () => now, advance: (ms: number) => (now += ms) };
}

describe('createInstantReplay', () => {
  it('redacts on ingest: a raw PHI string is never observable in the buffer', () => {
    const replay = createInstantReplay({
      redactor: emailRedactor(),
      ring: createRingBuffer<CaptureEvent>(),
      clock: fixedClock(),
    });
    replay.ingest(consoleEvent('e1', `contact ${PHI_EMAIL} for details`));

    const stored = replay.events();
    expect(stored).toHaveLength(1);
    const text = (stored[0]!.payload as ConsolePayload).text;
    expect(text).not.toContain(PHI_EMAIL);
  });

  it('withholds nothing, and counts nothing, for an event with no PHI', () => {
    const replay = createInstantReplay({
      redactor: emailRedactor(),
      ring: createRingBuffer<CaptureEvent>(),
      clock: fixedClock(),
    });
    replay.ingest(consoleEvent('e1', 'no phi here'));
    expect(replay.withheldEventCount()).toBe(0);
    expect(replay.evictedCount()).toBe(0);
    expect(replay.events()).toHaveLength(1);
  });

  it('increments withheldEventCount (redaction-only) for a dropped event, and leaves evictedCount at 0', () => {
    const replay = createInstantReplay({
      redactor: dropAllRedactor(),
      ring: createRingBuffer<CaptureEvent>(),
      clock: fixedClock(),
    });
    replay.ingest(dropEvent());
    expect(replay.withheldEventCount()).toBe(1);
    expect(replay.evictedCount()).toBe(0);
    expect(replay.events()).toHaveLength(0);
  });

  it('counts ring evictions in evictedCount only, leaving withheldEventCount (redaction-only) at 0', () => {
    const replay = createInstantReplay({
      redactor: emailRedactor(),
      ring: createRingBuffer<CaptureEvent>({ maxEvents: 1, maxBytes: 1024 * 1024 }),
      clock: fixedClock(),
    });
    replay.ingest(consoleEvent('e1', 'a'));
    replay.ingest(consoleEvent('e2', 'b'));
    expect(replay.evictedCount()).toBe(1);
    expect(replay.withheldEventCount()).toBe(0);
    expect(replay.events()).toHaveLength(1);
  });

  it('holds video chunks within the 120s window', () => {
    const clock = fixedClock(0);
    const replay = createInstantReplay({
      redactor: emailRedactor(),
      ring: createRingBuffer<CaptureEvent>(),
      clock,
    });
    replay.pushVideoChunk(new Uint8Array([1]));
    clock.advance(VIDEO_WINDOW_MS - 1);
    expect(replay.videoChunks()).toHaveLength(1);
  });

  it('releases video chunks past the window', () => {
    const clock = fixedClock(0);
    const replay = createInstantReplay({
      redactor: emailRedactor(),
      ring: createRingBuffer<CaptureEvent>(),
      clock,
    });
    replay.pushVideoChunk(new Uint8Array([1]));
    clock.advance(VIDEO_WINDOW_MS + 1);
    expect(replay.videoChunks()).toHaveLength(0);
  });

  it('releases only expired chunks, keeping ones still in the window', () => {
    const clock = fixedClock(0);
    const replay = createInstantReplay({
      redactor: emailRedactor(),
      ring: createRingBuffer<CaptureEvent>(),
      clock,
      windowMs: 100,
    });
    replay.pushVideoChunk(new Uint8Array([1])); // t=0
    clock.advance(60);
    replay.pushVideoChunk(new Uint8Array([2])); // t=60
    clock.advance(60); // now=120: first (t=0) is past window, second (t=60) is not
    expect(replay.videoChunks()).toHaveLength(1);
  });

  it('supports a custom window size', () => {
    const clock = fixedClock(0);
    const replay = createInstantReplay({
      redactor: emailRedactor(),
      ring: createRingBuffer<CaptureEvent>(),
      clock,
      windowMs: 10,
    });
    replay.pushVideoChunk(new Uint8Array([1]));
    clock.advance(11);
    expect(replay.videoChunks()).toHaveLength(0);
  });

  it('holds screenshots within the window and releases them past it', () => {
    const clock = fixedClock(0);
    const replay = createInstantReplay({
      redactor: emailRedactor(),
      ring: createRingBuffer<CaptureEvent>(),
      clock,
      windowMs: 100,
    });
    replay.pushScreenshot('data:image/png;base64,a');
    expect(replay.screenshots()).toEqual([{ dataUrl: 'data:image/png;base64,a', t: 0 }]);

    clock.advance(101);
    expect(replay.screenshots()).toHaveLength(0);
  });
});
