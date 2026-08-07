// Minimal static server for the nightly blur fixture — its own origin/port so
// it never collides with the PR-gating `e2e/` suite's server.
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const PUBLIC_DIR = path.join(here, 'public');
const PORT = Number(process.env.PORT ?? 5179);

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.map': 'application/json; charset=utf-8',
};

function safeJoin(root, rel) {
  const full = path.resolve(root, `.${path.posix.normalize(rel)}`);
  return full === root || full.startsWith(root + path.sep) ? full : null;
}

const server = http.createServer((req, res) => {
  const { pathname } = new URL(req.url, `http://${req.headers.host}`);
  const file = safeJoin(PUBLIC_DIR, pathname === '/' ? '/index.html' : pathname);
  if (!file || !fs.existsSync(file) || !fs.statSync(file).isFile()) {
    res.writeHead(404, { 'content-type': 'text/plain' });
    res.end('not found');
    return;
  }
  res.writeHead(200, {
    'content-type': MIME[path.extname(file)] ?? 'application/octet-stream',
    'cache-control': 'no-store',
  });
  res.end(fs.readFileSync(file));
});

server.listen(PORT, '127.0.0.1', () => {
  console.log(`nightly blur fixture server listening on http://127.0.0.1:${PORT}`);
});
