export const DEFAULT_BYTE_LIMIT = 2 * 1024 * 1024 * 1024; // 2 GB
export const DEFAULT_CAPTURE_CAP = 40;

export type BudgetPolicy = {
  byteLimit: number;
  captureCap: number;
};

export const DEFAULT_BUDGET_POLICY: BudgetPolicy = {
  byteLimit: DEFAULT_BYTE_LIMIT,
  captureCap: DEFAULT_CAPTURE_CAP,
};

export type StorageManager = {
  persist(): Promise<boolean>;
};

/**
 * Requests durable persistence before the first capture is written. Phase 0
 * has no server-side backstop, so an eviction under storage pressure would be
 * silent data loss of the only copy of a bug report.
 */
export async function requestPersistence(storage: StorageManager | undefined): Promise<boolean> {
  if (!storage) return false;
  return storage.persist();
}

export type CheckBudgetInput = {
  estimate: { usage: number; quota: number };
  captureCount: number;
  projectedBytes: number;
  limits?: Partial<BudgetPolicy>;
};

export type CheckBudgetResult =
  | { ok: true }
  | { ok: false; reason: 'quota' | 'capture-cap' | 'projected-overflow' };

/**
 * Phase 0 evicts nothing. At the cap the result is a refusal the caller
 * surfaces as an export-or-delete prompt — never a silent LRU delete.
 */
export function checkBudget(input: CheckBudgetInput): CheckBudgetResult {
  const policy = { ...DEFAULT_BUDGET_POLICY, ...input.limits };

  if (input.captureCount >= policy.captureCap) {
    return { ok: false, reason: 'capture-cap' };
  }

  if (input.estimate.usage >= policy.byteLimit || input.estimate.usage >= input.estimate.quota) {
    return { ok: false, reason: 'quota' };
  }

  if (input.estimate.usage + input.projectedBytes > policy.byteLimit) {
    return { ok: false, reason: 'projected-overflow' };
  }

  return { ok: true };
}
