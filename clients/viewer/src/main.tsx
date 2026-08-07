import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App.js';
import { bootstrapSession } from './identity.js';

async function main(): Promise<void> {
  const root = document.getElementById('root');
  if (!root) throw new Error('missing #root element');

  // Render-first-authenticate-second: only ever open the partition recorded
  // for the last authenticated identity. No recorded identity means no
  // capture data may be rendered, signed-out or not.
  const session = await bootstrapSession();

  if (!session) {
    createRoot(root).render(
      <StrictMode>
        <div className="app-signed-out">Sign in to view captures.</div>
      </StrictMode>,
    );
    return;
  }

  createRoot(root).render(
    <StrictMode>
      <App store={session.store} repo={session.repo} />
    </StrictMode>,
  );
}

void main();
