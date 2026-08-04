import { describe, expect, it, vi } from 'vitest';
import type { CaptureEvent, Clock, InteractionPayload } from '@htr/capture-core';
import {
  buildInteractionEvent,
  buildSelector,
  describeElement,
  startInteractionTrail,
  type DocumentLike,
} from './interaction.js';
import type { ElementLike } from '../lib/dom-types.js';

function fakeClock(t = 0): Clock {
  return { epoch: 0, now: () => t };
}

function fakeElement(overrides: Partial<ElementLike> = {}): ElementLike {
  return {
    tagName: 'DIV',
    getAttribute: () => null,
    closest: () => null,
    getBoundingClientRect: () => ({ x: 0, y: 0, width: 0, height: 0 }),
    textContent: null,
    querySelector: () => null,
    matches: () => false,
    ...overrides,
  };
}

describe('describeElement', () => {
  it('prefers aria-label as accessibleName', () => {
    const el = fakeElement({ getAttribute: (name) => (name === 'aria-label' ? 'Submit' : null) });
    expect(describeElement(el).accessibleName).toBe('Submit');
  });

  it('treats an empty aria-label as absent', () => {
    const el = fakeElement({ getAttribute: (name) => (name === 'aria-label' ? '' : null) });
    expect(describeElement(el).accessibleName).toBeNull();
  });

  it('reads labelText from the closest <label>', () => {
    const label = fakeElement({ textContent: ' Email ' });
    const el = fakeElement({ closest: (sel) => (sel === 'label' ? label : null) });
    expect(describeElement(el).labelText).toBe(' Email ');
  });

  it('has no labelText when no ancestor label exists', () => {
    const el = fakeElement();
    expect(describeElement(el).labelText).toBeNull();
  });

  it('reads testId from data-testid, empty string treated as absent', () => {
    const withId = fakeElement({ getAttribute: (n) => (n === 'data-testid' ? 'save-btn' : null) });
    expect(describeElement(withId).testId).toBe('save-btn');
    const emptyId = fakeElement({ getAttribute: (n) => (n === 'data-testid' ? '' : null) });
    expect(describeElement(emptyId).testId).toBeNull();
  });
});

describe('buildSelector', () => {
  it('prefers data-testid', () => {
    const el = fakeElement({ getAttribute: (n) => (n === 'data-testid' ? 'save-btn' : null) });
    expect(buildSelector(el)).toBe('[data-testid="save-btn"]');
  });

  it('falls back to id', () => {
    const el = fakeElement({ getAttribute: (n) => (n === 'id' ? 'main' : null) });
    expect(buildSelector(el)).toBe('#main');
  });

  it('falls back to tag.first-class', () => {
    const el = fakeElement({ tagName: 'BUTTON', getAttribute: (n) => (n === 'class' ? 'btn primary' : null) });
    expect(buildSelector(el)).toBe('button.btn');
  });

  it('falls back to bare tag when there is no id/class/testid', () => {
    const el = fakeElement({ tagName: 'SPAN' });
    expect(buildSelector(el)).toBe('span');
  });
});

describe('buildInteractionEvent', () => {
  it('builds an interaction CaptureEvent citing the descriptor-derived name and selector', () => {
    const el = fakeElement({
      tagName: 'INPUT',
      getAttribute: (n) => (n === 'data-testid' ? 'email' : null),
    });
    const event = buildInteractionEvent(
      { type: 'input', target: el, url: 'https://x.test', value: 'a@b.com' },
      'cap-1',
      fakeClock(12),
    );
    expect(event).toMatchObject({
      captureId: 'cap-1',
      t: 12,
      kind: 'interaction',
      payload: {
        type: 'input',
        targetName: 'email',
        targetSelector: '[data-testid="email"]',
        url: 'https://x.test',
        value: 'a@b.com',
      },
      redaction: { rulesApplied: [], fidelity: 'full' },
    });
  });
});

describe('startInteractionTrail', () => {
  function fakeDoc(): DocumentLike & { fire(type: string, target: unknown): void } {
    const listeners = new Map<string, ((event: { target: unknown }) => void)[]>();
    return {
      addEventListener(type, handler) {
        const list = listeners.get(type) ?? [];
        list.push(handler);
        listeners.set(type, list);
      },
      removeEventListener(type, handler) {
        const list = listeners.get(type) ?? [];
        const index = list.indexOf(handler);
        if (index >= 0) list.splice(index, 1);
      },
      fire(type, target) {
        for (const handler of listeners.get(type) ?? []) handler({ target });
      },
    };
  }

  it('emits a CaptureEvent for each tracked interaction type on a real element target', () => {
    const doc = fakeDoc();
    const emit = vi.fn<(event: CaptureEvent) => void>();
    startInteractionTrail(doc, 'cap-1', fakeClock(1), () => 'https://x.test', emit);

    const el = fakeElement({ tagName: 'A' });
    for (const type of ['click', 'input', 'keydown', 'scroll', 'submit']) {
      doc.fire(type, el);
    }
    expect(emit).toHaveBeenCalledTimes(5);
  });

  it('ignores events whose target is not element-like', () => {
    const doc = fakeDoc();
    const emit = vi.fn<(event: CaptureEvent) => void>();
    startInteractionTrail(doc, 'cap-1', fakeClock(), () => 'https://x.test', emit);
    doc.fire('click', null);
    doc.fire('click', {});
    expect(emit).not.toHaveBeenCalled();
  });

  it('reads a string `value` off the target when present', () => {
    const doc = fakeDoc();
    const emit = vi.fn<(event: CaptureEvent) => void>();
    startInteractionTrail(doc, 'cap-1', fakeClock(), () => 'https://x.test', emit);
    const el = { ...fakeElement(), value: 'typed' } as ElementLike & { value: string };
    doc.fire('input', el);
    expect((emit.mock.calls[0]![0].payload as InteractionPayload).value).toBe('typed');
  });

  it('treats a non-string `value` on the target as null', () => {
    const doc = fakeDoc();
    const emit = vi.fn<(event: CaptureEvent) => void>();
    startInteractionTrail(doc, 'cap-1', fakeClock(), () => 'https://x.test', emit);
    const el = { ...fakeElement(), value: 42 } as unknown as ElementLike;
    doc.fire('input', el);
    expect((emit.mock.calls[0]![0].payload as InteractionPayload).value).toBeNull();
  });

  it('returns a teardown that removes every listener it added', () => {
    const doc = fakeDoc();
    const emit = vi.fn<(event: CaptureEvent) => void>();
    const stop = startInteractionTrail(doc, 'cap-1', fakeClock(), () => 'https://x.test', emit);

    stop();
    const el = fakeElement({ tagName: 'A' });
    for (const type of ['click', 'input', 'keydown', 'scroll', 'submit']) {
      doc.fire(type, el);
    }
    expect(emit).not.toHaveBeenCalled();
  });
});
