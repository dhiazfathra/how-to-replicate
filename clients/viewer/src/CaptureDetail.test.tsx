import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import type { Capture } from '@htr/capture-core';
import { CaptureDetail } from './CaptureDetail.js';
import type { ViewerRepo } from './App.js';

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
      userAgent: 'ua',
      platform: 'p',
      viewport: { w: 0, h: 0 },
      devicePixelRatio: 1,
      locale: 'en',
      timezone: 'UTC',
      url: 'https://example.com',
    },
    metadata: {},
    doc: null,
    assets: [],
    withheldEventCount: 0,
    sync: { revision: 0, lastPushedAt: null, manifestComplete: true, dirtyFields: [] },
    ...overrides,
  };
}

describe('CaptureDetail', () => {
  // jsdom has no createObjectURL/revokeObjectURL. Stubbed for the whole file
  // (not per-test) so it stays in place through Testing Library's own
  // afterEach cleanup/unmount, which otherwise races a per-test unstub.
  beforeAll(() => {
    vi.stubGlobal('URL', {
      ...URL,
      createObjectURL: vi.fn().mockReturnValue('blob:mock'),
      revokeObjectURL: vi.fn(),
    });
  });

  afterAll(() => {
    vi.unstubAllGlobals();
  });

  it('renders with no video asset', async () => {
    const repo: ViewerRepo = { readEvents: vi.fn().mockResolvedValue([]), readAsset: vi.fn() };
    render(<CaptureDetail capture={makeCapture()} repo={repo} />);
    expect(await screen.findByText('No video for this capture')).toBeInTheDocument();
    expect(repo.readAsset).not.toHaveBeenCalled();
  });

  it('loads and plays the video asset when present', async () => {
    const capture = makeCapture({
      assets: [{ id: 'asset-1', captureId: 'cap-1', kind: 'video', mimeType: 'video/webm', sizeBytes: 3, chunkCount: 1, sha256: 'x' }],
    });
    const repo: ViewerRepo = {
      readEvents: vi.fn().mockResolvedValue([]),
      readAsset: vi.fn().mockResolvedValue(new Uint8Array([1, 2, 3])),
    };
    render(<CaptureDetail capture={capture} repo={repo} />);
    expect(await screen.findByTestId('player-video')).toHaveAttribute('src', 'blob:mock');
  });

  it('does not set a video src when the asset has no bytes', async () => {
    const capture = makeCapture({
      assets: [{ id: 'asset-1', captureId: 'cap-1', kind: 'video', mimeType: 'video/webm', sizeBytes: 0, chunkCount: 1, sha256: 'x' }],
    });
    const repo: ViewerRepo = {
      readEvents: vi.fn().mockResolvedValue([]),
      readAsset: vi.fn().mockResolvedValue(undefined),
    };
    render(<CaptureDetail capture={capture} repo={repo} />);
    expect(await screen.findByText('No video for this capture')).toBeInTheDocument();
  });

  it('unmounting before the asset load resolves does not throw', () => {
    const capture = makeCapture({
      assets: [{ id: 'asset-1', captureId: 'cap-1', kind: 'video', mimeType: 'video/webm', sizeBytes: 3, chunkCount: 1, sha256: 'x' }],
    });
    let resolveAsset!: (bytes: Uint8Array | undefined) => void;
    const repo: ViewerRepo = {
      readEvents: vi.fn().mockResolvedValue([]),
      readAsset: vi.fn(
        () => new Promise<Uint8Array | undefined>((resolve) => { resolveAsset = resolve; }),
      ),
    };
    const { unmount } = render(<CaptureDetail capture={capture} repo={repo} />);
    unmount();
    expect(() => resolveAsset(new Uint8Array([1]))).not.toThrow();
  });

  it('revokes the object URL on unmount after the asset resolves', async () => {
    const capture = makeCapture({
      assets: [{ id: 'asset-1', captureId: 'cap-1', kind: 'video', mimeType: 'video/webm', sizeBytes: 3, chunkCount: 1, sha256: 'x' }],
    });
    const repo: ViewerRepo = {
      readEvents: vi.fn().mockResolvedValue([]),
      readAsset: vi.fn().mockResolvedValue(new Uint8Array([1, 2, 3])),
    };
    const { unmount } = render(<CaptureDetail capture={capture} repo={repo} />);
    await screen.findByTestId('player-video');
    unmount();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:mock');
  });
});
