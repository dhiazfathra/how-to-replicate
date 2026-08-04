#!/usr/bin/env node
// Enforces invariant 5: packages/capture-core depends on nothing in clients/.
// Walks every file under packages/capture-core/src and fails if any import
// specifier resolves into clients/, names a client package, or references a
// browser-extension (chrome.*) API.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
export const ROOT = path.resolve(__dirname, '..');
export const TARGET_DIR = path.join(ROOT, 'packages/capture-core/src');

const CLIENT_PACKAGE_NAMES = ['@htr/extension', '@htr/viewer', '@htr/recording-link'];
const IMPORT_RE =
  /(?:import|export)\s[^;]*?from\s*['"]([^'"]+)['"]|import\s*['"]([^'"]+)['"]|require\(\s*['"]([^'"]+)['"]\s*\)/g;
const CHROME_API_RE = /\bchrome\.[A-Za-z_]/;

export function listFiles(dir) {
  if (!fs.existsSync(dir)) return [];
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...listFiles(full));
    } else if (/\.(ts|tsx|js|mjs)$/.test(entry.name)) {
      out.push(full);
    }
  }
  return out;
}

export function extractImports(source) {
  const specifiers = [];
  let match;
  IMPORT_RE.lastIndex = 0;
  while ((match = IMPORT_RE.exec(source)) !== null) {
    const specifier = match[1] ?? match[2] ?? match[3];
    if (specifier) specifiers.push(specifier);
  }
  return specifiers;
}

export function violationsForFile(file, source) {
  const violations = [];
  for (const specifier of extractImports(source)) {
    if (specifier.includes('/clients/') || specifier.startsWith('clients/')) {
      violations.push(`${file}: imports "${specifier}" which resolves into clients/`);
      continue;
    }
    if (CLIENT_PACKAGE_NAMES.includes(specifier)) {
      violations.push(`${file}: imports client package "${specifier}"`);
    }
  }
  if (CHROME_API_RE.test(source)) {
    violations.push(`${file}: references a chrome.* browser-extension API`);
  }
  return violations;
}

export function checkDeps(targetDir = TARGET_DIR) {
  const violations = [];
  for (const file of listFiles(targetDir)) {
    const source = fs.readFileSync(file, 'utf8');
    violations.push(...violationsForFile(path.relative(ROOT, file), source));
  }
  return violations;
}

export function main() {
  const violations = checkDeps();
  if (violations.length > 0) {
    console.error('check:deps failed — packages/capture-core must not depend on clients/:');
    for (const v of violations) console.error(`  - ${v}`);
    process.exit(1);
    return;
  }
  console.log('check:deps passed — capture-core has no client-side dependencies.');
}

/* v8 ignore start -- CLI entry guard: only true when run as `node check-deps.mjs`, exercised by pnpm check:deps, not unit-testable without spawning a subprocess (which coverage cannot instrument) */
if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
/* v8 ignore stop */
