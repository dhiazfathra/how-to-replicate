import { monotonicFactory } from 'ulid';

// Module-scope factory: ULIDs stay monotonic across the whole process, which
// is what makes timeline sort order need no tiebreaker (see plan conventions).
const defaultFactory = monotonicFactory();

/** Mint a new client-side ULID. Monotonic within this process. */
export function newId(): string {
  return defaultFactory();
}

/**
 * Create an isolated, monotonic ID factory — for tests that need deterministic,
 * reproducible IDs instead of the wall-clock-seeded module-scope factory.
 */
export function createIdFactory(seedTime?: number): () => string {
  const factory = monotonicFactory();
  return () => factory(seedTime);
}
