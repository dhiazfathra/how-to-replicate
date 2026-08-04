import type { CaptureEvent, ReplicationDoc } from '@htr/capture-core';
import { describe, expect, it } from 'vitest';
import { enrichDoc } from './enrich.js';
import type { LlmProvider } from './provider.js';

function event(id: string, overrides: Partial<CaptureEvent> = {}): CaptureEvent {
  return {
    id,
    captureId: 'cap-1',
    t: 0,
    kind: 'navigation',
    payload: { from: null, to: '/patients/123', trigger: 'load' },
    redaction: { rulesApplied: [], fidelity: 'full' },
    ...overrides,
  };
}

function baseDoc(): ReplicationDoc {
  return {
    title: 'Untitled capture',
    summary: '0 step(s) recorded deterministically.',
    steps: [],
    expected: null,
    actual: null,
    generator: 'deterministic',
    generatorModel: null,
  };
}

function providerReturning(text: string): LlmProvider {
  return { name: 'test-model', target: 'localhost', complete: () => Promise.resolve(text) };
}

function providerRejecting(error: Error): LlmProvider {
  return { name: 'test-model', target: 'localhost', complete: () => Promise.reject(error) };
}

describe('enrichDoc', () => {
  it('keeps the deterministic document untouched when the provider fails (network error)', async () => {
    const doc = baseDoc();
    const result = await enrichDoc({
      doc,
      events: [],
      provider: providerRejecting(new Error('network down')),
    });
    expect(result).toEqual(doc);
  });

  it('is non-fatal on a timeout-shaped rejection', async () => {
    const doc = baseDoc();
    const timeoutError = Object.assign(new Error('aborted'), { name: 'AbortError' });
    const result = await enrichDoc({ doc, events: [], provider: providerRejecting(timeoutError) });
    expect(result).toEqual(doc);
  });

  it('is non-fatal on malformed JSON', async () => {
    const doc = baseDoc();
    const result = await enrichDoc({ doc, events: [], provider: providerReturning('not json{') });
    expect(result).toEqual(doc);
  });

  it('discards the whole pass when the response is not an array', async () => {
    const doc = baseDoc();
    const result = await enrichDoc({
      doc,
      events: [],
      provider: providerReturning(JSON.stringify({ text: 'x', eventIds: [] })),
    });
    expect(result).toEqual(doc);
  });

  it('drops a step citing a fabricated event ID', async () => {
    const doc = baseDoc();
    const events = [event('e1')];
    const response = JSON.stringify([
      { text: 'Navigated to /patients/123', eventIds: ['e1', 'fabricated-id'] },
    ]);

    const result = await enrichDoc({ doc, events, provider: providerReturning(response) });

    expect(result).toEqual(doc);
    expect(result.generator).toBe('deterministic');
  });

  it('fuzzes several fabricated event IDs and drops every one', async () => {
    const doc = baseDoc();
    const events = [event('e1')];
    const fabricatedIds = ['x', 'e1-typo', '', 'e999', 'not-an-id-at-all'];
    const response = JSON.stringify(
      fabricatedIds.map((id) => ({ text: 'Navigated to /patients/123', eventIds: [id] })),
    );

    const result = await enrichDoc({ doc, events, provider: providerReturning(response) });

    expect(result).toEqual(doc);
  });

  it('drops a step citing a real event ID but describing content the event does not contain', async () => {
    const doc = baseDoc();
    const events = [event('e1')]; // navigates to /patients/123
    const response = JSON.stringify([
      { text: 'Clicked the Delete button', eventIds: ['e1'] },
    ]);

    const result = await enrichDoc({ doc, events, provider: providerReturning(response) });

    expect(result).toEqual(doc);
  });

  it('discards the entire pass when zero LLM steps survive', async () => {
    const doc = baseDoc();
    const events = [event('e1')];
    const response = JSON.stringify([
      { text: 'Clicked something unrelated', eventIds: ['e1'] },
      { text: 'Also unrelated', eventIds: ['missing'] },
    ]);

    const result = await enrichDoc({ doc, events, provider: providerReturning(response) });

    expect(result).toEqual(doc);
    expect(result.generator).toBe('deterministic');
  });

  it('keeps a surviving step, appends it, and marks the doc as llm-generated', async () => {
    const doc = baseDoc();
    const events = [event('e1')];
    const response = JSON.stringify([
      { text: 'Navigated to /patients/123 to view the record', eventIds: ['e1'] },
    ]);

    const result = await enrichDoc({ doc, events, provider: providerReturning(response) });

    expect(result.generator).toBe('llm');
    expect(result.generatorModel).toBe('test-model');
    expect(result.steps).toHaveLength(1);
    expect(result.steps[0]).toEqual({
      n: 1,
      text: 'Navigated to /patients/123 to view the record',
      eventIds: ['e1'],
      tVideo: null,
    });
  });

  it('appends surviving LLM steps after existing deterministic steps, keeping them unconditionally', async () => {
    const doc: ReplicationDoc = {
      ...baseDoc(),
      steps: [{ n: 1, text: 'Deterministic step', eventIds: ['e0'], tVideo: null }],
    };
    const events = [event('e1')];
    const response = JSON.stringify([
      { text: 'Navigated to /patients/123', eventIds: ['e1'] },
    ]);

    const result = await enrichDoc({ doc, events, provider: providerReturning(response) });

    expect(result.steps).toHaveLength(2);
    expect(result.steps[0]).toEqual(doc.steps[0]);
    expect(result.steps[1]?.n).toBe(2);
  });

  it('drops a candidate step with no cited event IDs', async () => {
    const doc = baseDoc();
    const events = [event('e1')];
    const response = JSON.stringify([{ text: 'Navigated to /patients/123', eventIds: [] }]);

    const result = await enrichDoc({ doc, events, provider: providerReturning(response) });

    expect(result).toEqual(doc);
  });

  it('drops non-object and shape-mismatched candidates', async () => {
    const doc = baseDoc();
    const events = [event('e1')];
    const response = JSON.stringify([
      null,
      'a string',
      42,
      { text: 123, eventIds: ['e1'] },
      { text: 'ok', eventIds: [1, 2] },
      { eventIds: ['e1'] },
    ]);

    const result = await enrichDoc({ doc, events, provider: providerReturning(response) });

    expect(result).toEqual(doc);
  });

  it('validates console, network, interaction, lifecycle, and annotation event kinds', async () => {
    const events: CaptureEvent[] = [
      event('console-1', {
        kind: 'console',
        payload: { level: 'error', text: 'TypeError: boom', stack: null },
      }),
      event('network-1', {
        kind: 'network',
        payload: {
          method: 'GET',
          url: '/api/patients/123',
          status: 500,
          requestHeaders: {},
          responseHeaders: {},
          requestBody: null,
          responseBody: null,
          bodyTruncated: false,
          bodyDropped: false,
          durationMs: 12,
          sizeBytes: 0,
        },
      }),
      event('interaction-1', {
        kind: 'interaction',
        payload: {
          type: 'click',
          targetName: 'Save changes',
          targetSelector: '#save',
          url: '/form',
          value: null,
        },
      }),
      event('lifecycle-1', {
        kind: 'lifecycle',
        payload: { transition: 'recording-started', detail: null },
      }),
      event('annotation-1', {
        kind: 'annotation',
        payload: { text: 'user noted this was slow' },
      }),
    ];

    const response = JSON.stringify([
      { text: 'Console error: TypeError: boom', eventIds: ['console-1'] },
      { text: 'Request to /api/patients/123 failed', eventIds: ['network-1'] },
      { text: 'Clicked Save changes', eventIds: ['interaction-1'] },
      { text: 'Lifecycle: recording-started', eventIds: ['lifecycle-1'] },
      { text: 'Annotation: user noted this was slow', eventIds: ['annotation-1'] },
      // mismatched content for each kind, must be dropped
      { text: 'Nothing to do with any of this', eventIds: ['console-1'] },
      { text: 'Nothing to do with any of this', eventIds: ['network-1'] },
      { text: 'Nothing to do with any of this', eventIds: ['interaction-1'] },
      { text: 'Nothing to do with any of this', eventIds: ['lifecycle-1'] },
      { text: 'Nothing to do with any of this', eventIds: ['annotation-1'] },
    ]);

    const result = await enrichDoc({ doc: baseDoc(), events, provider: providerReturning(response) });

    expect(result.generator).toBe('llm');
    expect(result.steps).toHaveLength(5);
  });
});
