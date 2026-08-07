import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { openCaptureDb, CaptureRepository, createCaptureStore } from '@htr/capture-core';
import { App } from './App.js';

async function main(): Promise<void> {
  const db = await openCaptureDb();
  const repo = new CaptureRepository(db);
  const store = createCaptureStore(repo);
  await store.hydrate();

  const root = document.getElementById('root');
  if (!root) throw new Error('missing #root element');

  createRoot(root).render(
    <StrictMode>
      <App store={store} repo={repo} />
    </StrictMode>,
  );
}

void main();
