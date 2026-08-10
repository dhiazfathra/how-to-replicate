import {
  CaptureRepository,
  createCaptureStore,
  ensureIdentityPartition,
  getRecordedIdentity,
  openIdentityPartitionDb,
  type CaptureStore,
  type Identity,
} from '@htr/capture-core';
import type { ViewerRepo } from './App.js';

export type Session = { store: CaptureStore; repo: ViewerRepo };

/**
 * Render-first-authenticate-second: opens whichever partition (if any) was
 * last authenticated locally, with no network round trip. Returns `null`
 * when no identity has ever been recorded on this device — the caller must
 * render a signed-out state rather than any capture data in that case.
 */
export async function bootstrapSession(): Promise<Session | null> {
  const identity = await getRecordedIdentity();
  if (!identity) return null;
  return openSession(identity);
}

/** Called on successful auth. Purges any prior partition, then opens the new one. */
export async function login(identity: Identity): Promise<Session> {
  return openSession(identity);
}

/** Called on logout. Purges the current partition and clears the pointer. */
export async function logout(): Promise<void> {
  await ensureIdentityPartition(null);
}

async function openSession(identity: Identity): Promise<Session> {
  const db = await openIdentityPartitionDb(identity);
  const repo = new CaptureRepository(db);
  const store = createCaptureStore(repo);
  await store.hydrate();
  return { store, repo };
}
