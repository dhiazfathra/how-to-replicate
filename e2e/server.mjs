// One static origin for the whole e2e suite, so the demo page and the built
// viewer share an IndexedDB. Node core only — no new dependency.
//
//   /                     the deliberately buggy demo app (+ /harness.js)
//   /viewer/              clients/viewer/dist/index.html
//   /assets/*             clients/viewer/dist/assets/* (the viewer's built
//                         index.html references these at an absolute path)
//   /api/patient-chart    always HTTP 500 — the demo app's real failing fetch
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const PUBLIC_DIR = path.join(here, 'public');
const VIEWER_DIR = path.resolve(here, '../clients/viewer/dist');
const PORT = Number(process.env.PORT ?? 5178);

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.map': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
};

function sendFile(res, file) {
  if (!fs.existsSync(file) || !fs.statSync(file).isFile()) {
    res.writeHead(404, { 'content-type': 'text/plain' });
    res.end(`not found: ${file}`);
    return;
  }
  res.writeHead(200, {
    'content-type': MIME[path.extname(file)] ?? 'application/octet-stream',
    'cache-control': 'no-store',
  });
  res.end(fs.readFileSync(file));
}

/** Join `rel` onto `root` without letting `..` escape it. */
function safeJoin(root, rel) {
  const full = path.resolve(root, `.${path.posix.normalize(rel)}`);
  return full === root || full.startsWith(root + path.sep) ? full : null;
}

const server = http.createServer((req, res) => {
  const { pathname } = new URL(req.url, `http://${req.headers.host}`);

  if (pathname.startsWith('/api/patient-chart')) {
    res.writeHead(500, { 'content-type': 'application/json; charset=utf-8' });
    res.end(JSON.stringify({ error: 'chart_service_unavailable', retryable: false }));
    return;
  }

  if (pathname === '/viewer' || pathname === '/viewer/') {
    sendFile(res, path.join(VIEWER_DIR, 'index.html'));
    return;
  }

  if (pathname.startsWith('/assets/')) {
    const file = safeJoin(VIEWER_DIR, pathname);
    if (file) sendFile(res, file);
    else res.writeHead(403).end();
    return;
  }

  const file = safeJoin(PUBLIC_DIR, pathname === '/' ? '/index.html' : pathname);
  if (file) sendFile(res, file);
  else res.writeHead(403).end();
});

server.listen(PORT, '127.0.0.1', () => {
  console.log(`e2e server listening on http://127.0.0.1:${PORT}`);
});
