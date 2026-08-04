import { describe, expect, it } from 'vitest';
import type { CaptureEvent, ConsolePayload } from '../types/event.js';
import { createRedactor } from './engine.js';
import { PHI_PATTERNS } from './patterns.js';
import { parseRuleset } from './ruleset.js';

function textEvent(text: string): CaptureEvent {
  return {
    id: 'e',
    captureId: 'c',
    t: 0,
    kind: 'console',
    payload: { level: 'log', text, stack: null },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}

function redactText(text: string): string {
  const redactor = createRedactor(parseRuleset({ version: '1', rules: [...PHI_PATTERNS] }));
  const outcome = redactor.redactEvent(textEvent(text));
  if (outcome.fidelity === 'dropped') throw new Error('unexpected drop');
  return (outcome.event.payload as ConsolePayload).text;
}

describe('PHI_PATTERNS', () => {
  it('parses cleanly as a ruleset', () => {
    expect(() => parseRuleset({ version: '1', rules: [...PHI_PATTERNS] })).not.toThrow();
  });

  it('redacts an Indonesian NIK (16 digits)', () => {
    expect(redactText('NIK: 3201012501990001')).toBe('NIK: [REDACTED:nik]');
  });

  it('redacts a BPJS number (13 digits)', () => {
    expect(redactText('BPJS 0001234567890')).toBe('BPJS [REDACTED:bpjs]');
  });

  it('redacts a medical record number', () => {
    expect(redactText('mrn MRN-123456 on file')).toBe('mrn [REDACTED:mrn] on file');
  });

  it('redacts an Indonesian phone number', () => {
    expect(redactText('call 081234567890')).toBe('call [REDACTED:phone-id]');
    expect(redactText('call +6281234567890')).toBe('call [REDACTED:phone-id]');
    expect(redactText('call 6281234567890')).toBe('call [REDACTED:phone-id]');
  });

  it('redacts an email address', () => {
    expect(redactText('contact jane.doe@example.com now')).toBe('contact [REDACTED:email] now');
  });

  it('redacts an ISO date of birth', () => {
    expect(redactText('dob 1990-01-25')).toBe('dob [REDACTED:dob]');
  });

  it('redacts a DD/MM/YYYY date of birth', () => {
    expect(redactText('dob 25/01/1990')).toBe('dob [REDACTED:dob]');
  });

  it('leaves ordinary text untouched', () => {
    expect(redactText('nothing sensitive here')).toBe('nothing sensitive here');
  });
});
