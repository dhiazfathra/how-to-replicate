import { describe, expect, it } from 'vitest';
import { targetName, type ElementDescriptor } from './naming.js';

const base: ElementDescriptor = {
  accessibleName: null,
  labelText: null,
  textContent: null,
  testId: null,
  selector: '#save-btn',
};

describe('targetName', () => {
  it('prefers the accessible name over everything else', () => {
    const el: ElementDescriptor = {
      ...base,
      accessibleName: 'Save changes',
      labelText: 'Label',
      textContent: 'Text',
      testId: 'save-btn',
    };
    expect(targetName(el)).toBe('Save changes');
  });

  it('falls back to the associated label when there is no accessible name', () => {
    const el: ElementDescriptor = {
      ...base,
      labelText: 'Email address',
      textContent: 'Text',
      testId: 'save-btn',
    };
    expect(targetName(el)).toBe('Email address');
  });

  it('falls back to trimmed text content when there is no label', () => {
    const el: ElementDescriptor = {
      ...base,
      textContent: '  Save changes  ',
      testId: 'save-btn',
    };
    expect(targetName(el)).toBe('Save changes');
  });

  it('falls back to data-testid when there is no usable text content', () => {
    const el: ElementDescriptor = { ...base, testId: 'save-btn' };
    expect(targetName(el)).toBe('save-btn');
  });

  it('falls back to the CSS selector when nothing else is available', () => {
    expect(targetName(base)).toBe('#save-btn');
  });

  it('treats whitespace-only values as absent at every fallback level', () => {
    const el: ElementDescriptor = {
      accessibleName: '   ',
      labelText: '   ',
      textContent: '   ',
      testId: '   ',
      selector: '#save-btn',
    };
    expect(targetName(el)).toBe('#save-btn');
  });
});
