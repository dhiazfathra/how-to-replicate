import type { Capture } from '../types/capture.js';
import type { CaptureEvent } from '../types/event.js';
import type { AssetRef } from '../types/asset.js';
import { assertReady } from '../pipeline/gate.js';
import { renderMarkdown } from './markdown.js';

export type HtrAsset = {
  ref: AssetRef;
  bytes: Uint8Array;
};

type ZipEntry = {
  name: string;
  bytes: Uint8Array;
  crc: number;
  offset: number;
};

const LOCAL_FILE_HEADER_SIG = 0x04034b50;
const CENTRAL_DIR_SIG = 0x02014b50;
const END_OF_CENTRAL_DIR_SIG = 0x06054b50;

// Standard CRC-32 (IEEE 802.3) table, built once and reused — no runtime
// dependency needed, and no reliance on a specific Node global being typed.
const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n += 1) {
    let c = n;
    for (let k = 0; k < 8; k += 1) {
      c = c & 1 ? (0xedb88320 ^ (c >>> 1)) : c >>> 1;
    }
    table[n] = c >>> 0;
  }
  return table;
})();

function crc32(bytes: Uint8Array): number {
  let crc = 0xffffffff;
  for (let i = 0; i < bytes.byteLength; i += 1) {
    crc = CRC_TABLE[(crc ^ bytes[i]!) & 0xff]! ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function textToBytes(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

function concat(chunks: Uint8Array[]): Uint8Array {
  const total = chunks.reduce((sum, c) => sum + c.byteLength, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    out.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return out;
}

/**
 * Minimal stored-only (no compression) ZIP writer: just enough of the
 * format for local-file-header + central-directory + end-of-central-dir to
 * round-trip through any standard unzip. No compression means no deflate
 * implementation to hand-roll — the trade is a larger file, which is fine
 * for a local .htr export.
 */
function writeZip(files: { name: string; bytes: Uint8Array }[]): Uint8Array {
  const localParts: Uint8Array[] = [];
  const entries: ZipEntry[] = [];
  let offset = 0;

  for (const file of files) {
    const nameBytes = textToBytes(file.name);
    const crc = crc32(file.bytes);
    const header = new DataView(new ArrayBuffer(30));
    header.setUint32(0, LOCAL_FILE_HEADER_SIG, true);
    header.setUint16(4, 20, true); // version needed
    header.setUint16(6, 0, true); // flags
    header.setUint16(8, 0, true); // method: stored
    header.setUint16(10, 0, true); // mod time
    header.setUint16(12, 0, true); // mod date
    header.setUint32(14, crc, true);
    header.setUint32(18, file.bytes.byteLength, true); // compressed size
    header.setUint32(22, file.bytes.byteLength, true); // uncompressed size
    header.setUint16(26, nameBytes.byteLength, true);
    header.setUint16(28, 0, true); // extra field length

    entries.push({ name: file.name, bytes: file.bytes, crc, offset });
    const headerBytes = new Uint8Array(header.buffer);
    localParts.push(headerBytes, nameBytes, file.bytes);
    offset += headerBytes.byteLength + nameBytes.byteLength + file.bytes.byteLength;
  }

  const centralStart = offset;
  const centralParts: Uint8Array[] = [];
  for (const entry of entries) {
    const nameBytes = textToBytes(entry.name);
    const header = new DataView(new ArrayBuffer(46));
    header.setUint32(0, CENTRAL_DIR_SIG, true);
    header.setUint16(4, 20, true); // version made by
    header.setUint16(6, 20, true); // version needed
    header.setUint16(8, 0, true); // flags
    header.setUint16(10, 0, true); // method: stored
    header.setUint16(12, 0, true); // mod time
    header.setUint16(14, 0, true); // mod date
    header.setUint32(16, entry.crc, true);
    header.setUint32(20, entry.bytes.byteLength, true);
    header.setUint32(24, entry.bytes.byteLength, true);
    header.setUint16(28, nameBytes.byteLength, true);
    header.setUint16(30, 0, true); // extra length
    header.setUint16(32, 0, true); // comment length
    header.setUint16(34, 0, true); // disk number start
    header.setUint16(36, 0, true); // internal attrs
    header.setUint32(38, 0, true); // external attrs
    header.setUint32(42, entry.offset, true); // local header offset
    centralParts.push(new Uint8Array(header.buffer), nameBytes);
  }
  const centralBytes = concat(centralParts);

  const end = new DataView(new ArrayBuffer(22));
  end.setUint32(0, END_OF_CENTRAL_DIR_SIG, true);
  end.setUint16(4, 0, true); // disk number
  end.setUint16(6, 0, true); // disk with central dir
  end.setUint16(8, entries.length, true); // entries on this disk
  end.setUint16(10, entries.length, true); // total entries
  end.setUint32(12, centralBytes.byteLength, true); // central dir size
  end.setUint32(16, centralStart, true); // central dir offset
  end.setUint16(20, 0, true); // comment length

  return concat([...localParts, centralBytes, new Uint8Array(end.buffer)]);
}

/**
 * Build a zip-shaped `.htr` export bundle: `capture.json`, `timeline.json`,
 * `document.md`, and the asset blobs under `assets/`. Throws unless
 * `capture` is `ready` — the gate that keeps an unredacted capture from ever
 * becoming an exportable artifact.
 */
export function buildHtrBundle(
  capture: Capture,
  events: CaptureEvent[],
  assets: HtrAsset[] = [],
): Uint8Array {
  assertReady(capture);

  const files = [
    { name: 'capture.json', bytes: textToBytes(JSON.stringify(capture, null, 2)) },
    { name: 'timeline.json', bytes: textToBytes(JSON.stringify(events, null, 2)) },
    { name: 'document.md', bytes: textToBytes(renderMarkdown(capture)) },
    ...assets.map((asset) => ({
      name: `assets/${asset.ref.id}`,
      bytes: asset.bytes,
    })),
  ];

  return writeZip(files);
}
