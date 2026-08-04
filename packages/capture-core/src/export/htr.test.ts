import { describe, expect, it } from 'vitest';
import { buildHtrBundle, type HtrAsset } from './htr.js';
import type { Capture, CaptureState } from '../types/capture.js';
import type { CaptureEvent } from '../types/event.js';
import type { ReplicationDoc } from '../types/doc.js';

const doc: ReplicationDoc = {
  title: 'Bug repro',
  summary: '1 step(s) recorded deterministically.',
  steps: [{ n: 1, text: 'Clicked "Save"', eventIds: ['ev-1'], tVideo: null }],
  expected: null,
  actual: null,
  generator: 'deterministic',
  generatorModel: null,
};

function makeCapture(overrides: Partial<Capture> = {}): Capture {
  return {
    id: 'cap-1',
    workspaceId: null,
    projectId: null,
    source: 'extension',
    state: 'ready',
    fidelity: 'full',
    createdAt: '2026-08-04T00:00:00.000Z',
    epoch: 0,
    env: {
      userAgent: 'test-agent',
      platform: 'test',
      viewport: { w: 100, h: 100 },
      devicePixelRatio: 1,
      locale: 'en-US',
      timezone: 'UTC',
      url: 'https://example.com',
    },
    metadata: {},
    doc,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: false, dirtyFields: [] },
    ...overrides,
  };
}

const events: CaptureEvent[] = [
  {
    id: 'ev-1',
    captureId: 'cap-1',
    t: 0,
    kind: 'navigation',
    payload: { from: null, to: 'https://example.com', trigger: 'load' },
    redaction: { rulesApplied: [], fidelity: 'full' },
  },
];

/** Minimal manual ZIP reader for round-trip verification only — reads the
 * end-of-central-directory + central directory to locate each stored entry,
 * then slices its bytes straight from the local file header (method 0 means
 * no decompression is needed). Not a general-purpose unzip. */
function readStoredZip(bytes: Uint8Array): Map<string, Uint8Array> {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const eocdSig = 0x06054b50;
  let eocdOffset = -1;
  for (let i = bytes.byteLength - 22; i >= 0; i -= 1) {
    if (view.getUint32(i, true) === eocdSig) {
      eocdOffset = i;
      break;
    }
  }
  if (eocdOffset < 0) throw new Error('not a zip: no end-of-central-directory record');

  const entryCount = view.getUint16(eocdOffset + 10, true);
  const centralDirOffset = view.getUint32(eocdOffset + 16, true);

  const out = new Map<string, Uint8Array>();
  let cursor = centralDirOffset;
  for (let i = 0; i < entryCount; i += 1) {
    if (view.getUint32(cursor, true) !== 0x02014b50) throw new Error('bad central directory entry');
    const compSize = view.getUint32(cursor + 20, true);
    const nameLen = view.getUint16(cursor + 28, true);
    const extraLen = view.getUint16(cursor + 30, true);
    const commentLen = view.getUint16(cursor + 32, true);
    const localOffset = view.getUint32(cursor + 42, true);
    const name = new TextDecoder().decode(bytes.subarray(cursor + 46, cursor + 46 + nameLen));

    const localNameLen = view.getUint16(localOffset + 26, true);
    const localExtraLen = view.getUint16(localOffset + 28, true);
    const dataStart = localOffset + 30 + localNameLen + localExtraLen;
    out.set(name, bytes.slice(dataStart, dataStart + compSize));

    cursor += 46 + nameLen + extraLen + commentLen;
  }
  return out;
}

describe('buildHtrBundle', () => {
  it('produces a zip whose entries round-trip through a standard unzip', () => {
    const asset: HtrAsset = {
      ref: { id: 'asset-1', captureId: 'cap-1', kind: 'screenshot', mimeType: 'image/png', sizeBytes: 4, chunkCount: 1, sha256: 'x' },
      bytes: new Uint8Array([1, 2, 3, 4]),
    };
    const bundle = buildHtrBundle(makeCapture(), events, [asset]);
    const files = readStoredZip(bundle);

    expect(new TextDecoder().decode(files.get('capture.json'))).toContain('"id": "cap-1"');
    expect(JSON.parse(new TextDecoder().decode(files.get('timeline.json')))).toHaveLength(1);
    expect(new TextDecoder().decode(files.get('document.md'))).toContain('Bug repro');
    expect(files.get('assets/asset-1')).toEqual(new Uint8Array([1, 2, 3, 4]));
  });

  it('builds an empty-asset bundle without error', () => {
    const bundle = buildHtrBundle(makeCapture(), events);
    const files = readStoredZip(bundle);
    expect(files.size).toBe(3);
  });

  const nonReady: CaptureState[] = ['recording', 'redacting', 'composing', 'failed', 'expired'];
  it.each(nonReady)('throws when the capture is %s', (state) => {
    expect(() => buildHtrBundle(makeCapture({ state }), events)).toThrow(/not ready/);
  });
});
