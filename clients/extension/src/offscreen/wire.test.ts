import { describe, expect, it, vi } from 'vitest';
import { BLUR_REGIONS_MESSAGE_TYPE } from '../content/blur-regions.js';
import { createBlurRegionsListener, createFrameSource, type VideoElementLike } from './wire.js';

describe('createBlurRegionsListener', () => {
  it('forwards regions from a matching-capture message', () => {
    const updateRegions = vi.fn();
    const listener = createBlurRegionsListener('cap-1', { updateRegions });

    listener({ type: BLUR_REGIONS_MESSAGE_TYPE, captureId: 'cap-1', regions: [{ x: 1, y: 2, width: 3, height: 4 }] });

    expect(updateRegions).toHaveBeenCalledWith([{ x: 1, y: 2, width: 3, height: 4 }]);
  });

  it('ignores a message for a different capture', () => {
    const updateRegions = vi.fn();
    const listener = createBlurRegionsListener('cap-1', { updateRegions });

    listener({ type: BLUR_REGIONS_MESSAGE_TYPE, captureId: 'cap-2', regions: [] });

    expect(updateRegions).not.toHaveBeenCalled();
  });

  it('ignores a message of a different type', () => {
    const updateRegions = vi.fn();
    const listener = createBlurRegionsListener('cap-1', { updateRegions });

    listener({ type: 'htr:something-else', captureId: 'cap-1', regions: [] });

    expect(updateRegions).not.toHaveBeenCalled();
  });

  it.each([null, undefined, 'string', 42])('ignores a non-object message %p', (message) => {
    const updateRegions = vi.fn();
    const listener = createBlurRegionsListener('cap-1', { updateRegions });

    listener(message);

    expect(updateRegions).not.toHaveBeenCalled();
  });
});

describe('createFrameSource', () => {
  function fakeVideo(): VideoElementLike & { playCalls: number } {
    return {
      srcObject: null,
      videoWidth: 0,
      videoHeight: 0,
      playCalls: 0,
      play(): Promise<void> {
        this.playCalls += 1;
        return Promise.resolve();
      },
    };
  }

  it('attaches the stream and starts playback', () => {
    const video = fakeVideo();
    createFrameSource(video, 'the-stream');

    expect(video.srcObject).toBe('the-stream');
    expect(video.playCalls).toBe(1);
  });

  it('reads width/height/frame live off the video element on every access', () => {
    const video = fakeVideo();
    const source = createFrameSource(video, 'the-stream');

    expect(source.width).toBe(0);
    expect(source.height).toBe(0);

    video.videoWidth = 1920;
    video.videoHeight = 1080;

    expect(source.width).toBe(1920);
    expect(source.height).toBe(1080);
    expect(source.frame).toBe(video);
  });
});
