import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { checkDeps, extractImports, violationsForFile, listFiles, main, ROOT } from './check-deps.mjs';

describe('check-deps', () => {
  let dir;

  beforeEach(() => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), 'check-deps-'));
  });

  afterEach(() => {
    fs.rmSync(dir, { recursive: true, force: true });
  });

  it('returns no violations for a clean file', () => {
    fs.writeFileSync(path.join(dir, 'a.ts'), "import { x } from './b.js';\nexport const y = x;\n");
    expect(checkDeps(dir)).toEqual([]);
  });

  it('flags an import that resolves into clients/', () => {
    fs.writeFileSync(path.join(dir, 'a.ts'), "import { x } from '../../../clients/viewer/src/foo.js';\n");
    const result = checkDeps(dir);
    expect(result).toHaveLength(1);
    expect(result[0]).toContain('clients/');
  });

  it('flags a relative import starting with clients/', () => {
    fs.writeFileSync(path.join(dir, 'a.ts'), "import { x } from 'clients/viewer';\n");
    expect(checkDeps(dir)).toHaveLength(1);
  });

  it('flags a named client package import', () => {
    fs.writeFileSync(path.join(dir, 'a.ts'), "import { x } from '@htr/extension';\n");
    const result = checkDeps(dir);
    expect(result).toHaveLength(1);
    expect(result[0]).toContain('@htr/extension');
  });

  it('flags each client package name', () => {
    for (const name of ['@htr/extension', '@htr/viewer', '@htr/recording-link']) {
      fs.writeFileSync(path.join(dir, 'a.ts'), `import { x } from '${name}';\n`);
      expect(checkDeps(dir)).toHaveLength(1);
    }
  });

  it('flags chrome.* API usage', () => {
    fs.writeFileSync(path.join(dir, 'a.ts'), 'chrome.runtime.sendMessage({});\n');
    const result = checkDeps(dir);
    expect(result).toHaveLength(1);
    expect(result[0]).toContain('chrome.*');
  });

  it('flags both an import violation and a chrome.* usage in one file', () => {
    fs.writeFileSync(
      path.join(dir, 'a.ts'),
      "import { x } from '@htr/viewer';\nchrome.storage.local.get();\n",
    );
    expect(checkDeps(dir)).toHaveLength(2);
  });

  it('handles bare import (side-effect only) specifiers', () => {
    fs.writeFileSync(path.join(dir, 'a.ts'), "import '@htr/extension';\n");
    expect(checkDeps(dir)).toHaveLength(1);
  });

  it('handles require() specifiers', () => {
    fs.writeFileSync(path.join(dir, 'a.ts'), "const x = require('@htr/extension');\n");
    expect(checkDeps(dir)).toHaveLength(1);
  });

  it('recurses into subdirectories', () => {
    fs.mkdirSync(path.join(dir, 'sub'));
    fs.writeFileSync(path.join(dir, 'sub', 'b.ts'), "import { x } from '@htr/viewer';\n");
    expect(checkDeps(dir)).toHaveLength(1);
  });

  it('ignores non-source files', () => {
    fs.writeFileSync(path.join(dir, 'readme.md'), "import { x } from '@htr/viewer';\n");
    expect(checkDeps(dir)).toEqual([]);
  });

  it('returns no violations when target directory does not exist', () => {
    expect(checkDeps(path.join(dir, 'does-not-exist'))).toEqual([]);
  });

  it('extractImports returns empty array for source with no imports', () => {
    expect(extractImports('const a = 1;')).toEqual([]);
  });

  it('violationsForFile returns empty array for clean source', () => {
    expect(violationsForFile('a.ts', 'const a = 1;')).toEqual([]);
  });

  it('listFiles only collects supported extensions', () => {
    fs.writeFileSync(path.join(dir, 'a.ts'), '');
    fs.writeFileSync(path.join(dir, 'a.json'), '');
    fs.writeFileSync(path.join(dir, 'a.mjs'), '');
    expect(listFiles(dir).map((f) => path.basename(f)).sort()).toEqual(['a.mjs', 'a.ts']);
  });
});

describe('main', () => {
  const captureCoreSrc = path.join(ROOT, 'packages/capture-core/src');
  const offendingFile = path.join(captureCoreSrc, '__check-deps-main-test.ts');

  afterEach(() => {
    fs.rmSync(offendingFile, { force: true });
    vi.restoreAllMocks();
  });

  it('logs a pass message and does not exit when the tree is clean', () => {
    const log = vi.spyOn(console, 'log').mockImplementation(() => {});
    const exit = vi.spyOn(process, 'exit').mockImplementation(() => undefined);
    main();
    expect(log).toHaveBeenCalledWith(expect.stringContaining('check:deps passed'));
    expect(exit).not.toHaveBeenCalled();
  });

  it('logs violations and exits 1 when capture-core imports a client package', () => {
    fs.writeFileSync(offendingFile, "import { x } from '@htr/viewer';\n");
    const log = vi.spyOn(console, 'log').mockImplementation(() => {});
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    const exit = vi.spyOn(process, 'exit').mockImplementation(() => undefined);
    main();
    expect(exit).toHaveBeenCalledWith(1);
    expect(error).toHaveBeenCalledWith(expect.stringContaining('check:deps failed'));
    expect(error).toHaveBeenCalledWith(expect.stringContaining('@htr/viewer'));
    expect(log).not.toHaveBeenCalled();
  });
});
