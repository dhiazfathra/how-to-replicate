import { describe, expect, it, vi } from 'vitest';
import { createObservable, createRecordStore } from './observable.js';

describe('createObservable', () => {
  it('returns the initial value', () => {
    expect(createObservable(1).get()).toBe(1);
  });

  it('updates the value and notifies subscribers on set', () => {
    const obs = createObservable(1);
    const fn = vi.fn();
    obs.subscribe(fn);
    obs.set(2);
    expect(obs.get()).toBe(2);
    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith(2);
  });

  it('notifies synchronously', () => {
    const obs = createObservable(0);
    let seen = -1;
    obs.subscribe((v) => {
      seen = v;
    });
    obs.set(5);
    expect(seen).toBe(5); // no microtask/timer needed
  });

  it('does not notify when the new value is Object.is-equal to the current one', () => {
    const obs = createObservable(NaN);
    const fn = vi.fn();
    obs.subscribe(fn);
    obs.set(NaN); // Object.is(NaN, NaN) === true, unlike ===
    expect(fn).not.toHaveBeenCalled();
  });

  it('does not notify on a reference-equal object set', () => {
    const value = { a: 1 };
    const obs = createObservable(value);
    const fn = vi.fn();
    obs.subscribe(fn);
    obs.set(value);
    expect(fn).not.toHaveBeenCalled();
  });

  it('stops notifying an unsubscribed callback', () => {
    const obs = createObservable(0);
    const fn = vi.fn();
    const unsubscribe = obs.subscribe(fn);
    unsubscribe();
    obs.set(1);
    expect(fn).not.toHaveBeenCalled();
  });

  it('supports multiple independent subscribers', () => {
    const obs = createObservable(0);
    const a = vi.fn();
    const b = vi.fn();
    obs.subscribe(a);
    const unsubB = obs.subscribe(b);
    obs.set(1);
    unsubB();
    obs.set(2);
    expect(a).toHaveBeenCalledTimes(2);
    expect(b).toHaveBeenCalledTimes(1);
  });
});

describe('createRecordStore', () => {
  it('lazily creates a per-entity per-field observable', () => {
    const store = createRecordStore<number>();
    const cell = store.field('e1', 'count', 0);
    expect(cell.get()).toBe(0);
  });

  it('returns the same observable for the same entity+field', () => {
    const store = createRecordStore<number>();
    const a = store.field('e1', 'count', 0);
    const b = store.field('e1', 'count', 99); // initial ignored on second call
    expect(a).toBe(b);
    expect(b.get()).toBe(0);
  });

  it('keeps distinct fields and entities independent', () => {
    const store = createRecordStore<number>();
    const count = store.field('e1', 'count', 0);
    const other = store.field('e1', 'other', 10);
    const e2count = store.field('e2', 'count', 5);
    count.set(1);
    expect(other.get()).toBe(10);
    expect(e2count.get()).toBe(5);
  });

  it('deleteEntity drops all fields for that entity, so a later field() call re-initializes', () => {
    const store = createRecordStore<number>();
    const cell = store.field('e1', 'count', 0);
    cell.set(7);
    store.deleteEntity('e1');
    const fresh = store.field('e1', 'count', 0);
    expect(fresh.get()).toBe(0);
    expect(fresh).not.toBe(cell);
  });
});
