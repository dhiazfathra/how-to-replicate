import { describe, expect, it, vi } from 'vitest';
import {
  checkBudget,
  requestPersistence,
  DEFAULT_BYTE_LIMIT,
  DEFAULT_CAPTURE_CAP,
} from './budget.js';

describe('requestPersistence', () => {
  it('calls navigator.storage.persist() when the API is present', async () => {
    const persist = vi.fn().mockResolvedValue(true);
    await expect(requestPersistence({ persist })).resolves.toBe(true);
    expect(persist).toHaveBeenCalledOnce();
  });

  it('resolves false without calling anything when the API is absent', async () => {
    await expect(requestPersistence(undefined)).resolves.toBe(false);
  });

  it('propagates a false result from persist()', async () => {
    await expect(requestPersistence({ persist: () => Promise.resolve(false) })).resolves.toBe(
      false,
    );
  });
});

describe('checkBudget', () => {
  const baseEstimate = { usage: 0, quota: DEFAULT_BYTE_LIMIT * 10 };

  it('is ok under every limit', () => {
    expect(
      checkBudget({ estimate: baseEstimate, captureCount: 0, projectedBytes: 1_000 }),
    ).toEqual({ ok: true });
  });

  it('refuses at the capture cap', () => {
    expect(
      checkBudget({
        estimate: baseEstimate,
        captureCount: DEFAULT_CAPTURE_CAP,
        projectedBytes: 0,
      }),
    ).toEqual({ ok: false, reason: 'capture-cap' });
  });

  it('refuses past the capture cap', () => {
    expect(
      checkBudget({
        estimate: baseEstimate,
        captureCount: DEFAULT_CAPTURE_CAP + 1,
        projectedBytes: 0,
      }),
    ).toEqual({ ok: false, reason: 'capture-cap' });
  });

  it('refuses when usage already meets the byte limit', () => {
    expect(
      checkBudget({
        estimate: { usage: DEFAULT_BYTE_LIMIT, quota: DEFAULT_BYTE_LIMIT * 10 },
        captureCount: 0,
        projectedBytes: 0,
      }),
    ).toEqual({ ok: false, reason: 'quota' });
  });

  it('refuses when usage already meets the browser-reported quota', () => {
    expect(
      checkBudget({
        estimate: { usage: 500, quota: 500 },
        captureCount: 0,
        projectedBytes: 0,
      }),
    ).toEqual({ ok: false, reason: 'quota' });
  });

  it('refuses when the projected write would overflow the byte limit', () => {
    expect(
      checkBudget({
        estimate: { usage: DEFAULT_BYTE_LIMIT - 100, quota: DEFAULT_BYTE_LIMIT * 10 },
        captureCount: 0,
        projectedBytes: 200,
      }),
    ).toEqual({ ok: false, reason: 'projected-overflow' });
  });

  it('honors an overridden policy', () => {
    expect(
      checkBudget({
        estimate: { usage: 0, quota: 1_000_000 },
        captureCount: 2,
        projectedBytes: 0,
        limits: { captureCap: 2 },
      }),
    ).toEqual({ ok: false, reason: 'capture-cap' });
  });

  it('checks capture cap before byte limits', () => {
    expect(
      checkBudget({
        estimate: { usage: DEFAULT_BYTE_LIMIT, quota: DEFAULT_BYTE_LIMIT * 10 },
        captureCount: DEFAULT_CAPTURE_CAP,
        projectedBytes: 0,
      }),
    ).toEqual({ ok: false, reason: 'capture-cap' });
  });
});
