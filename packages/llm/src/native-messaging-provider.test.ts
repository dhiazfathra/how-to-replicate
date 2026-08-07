import { describe, expect, it, vi } from 'vitest';
import { createNativeMessagingProvider, type NativePort } from './native-messaging-provider.js';

function fakePort(): NativePort & {
  emitMessage: (message: unknown) => void;
  emitDisconnect: () => void;
} {
  const messageListeners: Array<(message: unknown) => void> = [];
  const disconnectListeners: Array<() => void> = [];
  return {
    postMessage: vi.fn(),
    onMessage: { addListener: (cb) => messageListeners.push(cb) },
    onDisconnect: { addListener: (cb) => disconnectListeners.push(cb) },
    disconnect: vi.fn(),
    emitMessage: (message) => messageListeners.forEach((cb) => cb(message)),
    emitDisconnect: () => disconnectListeners.forEach((cb) => cb()),
  };
}

describe('createNativeMessagingProvider', () => {
  it('always reports target native-messaging', () => {
    const port = fakePort();
    const provider = createNativeMessagingProvider({ connect: () => port, hostName: 'com.htr.host' });
    expect(provider.target).toBe('native-messaging');
    expect(provider.name).toBe('native-messaging:com.htr.host');
  });

  it('resolves with the text of the reply message', async () => {
    const port = fakePort();
    const provider = createNativeMessagingProvider({ connect: () => port, hostName: 'h' });

    const promise = provider.complete({ prompt: 'p', timeoutMs: 1000 });
    port.emitMessage({ text: 'reply' });

    await expect(promise).resolves.toBe('reply');
    expect(port.disconnect).toHaveBeenCalled();
  });

  it('rejects on a malformed reply message', async () => {
    const port = fakePort();
    const provider = createNativeMessagingProvider({ connect: () => port, hostName: 'h' });

    const promise = provider.complete({ prompt: 'p', timeoutMs: 1000 });
    port.emitMessage({ garbage: true });

    await expect(promise).rejects.toThrow(/malformed/);
  });

  it('rejects when the port disconnects before a reply', async () => {
    const port = fakePort();
    const provider = createNativeMessagingProvider({ connect: () => port, hostName: 'h' });

    const promise = provider.complete({ prompt: 'p', timeoutMs: 1000 });
    port.emitDisconnect();

    await expect(promise).rejects.toThrow(/disconnected/);
  });

  it('rejects and disconnects on timeout', async () => {
    vi.useFakeTimers();
    const port = fakePort();
    const provider = createNativeMessagingProvider({ connect: () => port, hostName: 'h' });

    const promise = provider.complete({ prompt: 'p', timeoutMs: 10 });
    const assertion = expect(promise).rejects.toThrow(/timed out/);
    await vi.advanceTimersByTimeAsync(10);
    await assertion;
    expect(port.disconnect).toHaveBeenCalled();
    vi.useRealTimers();
  });

  it('ignores a late disconnect after the message already resolved', async () => {
    const port = fakePort();
    const provider = createNativeMessagingProvider({ connect: () => port, hostName: 'h' });

    const promise = provider.complete({ prompt: 'p', timeoutMs: 1000 });
    port.emitMessage({ text: 'reply' });
    port.emitDisconnect();

    await expect(promise).resolves.toBe('reply');
  });

  it('ignores a timeout that fires after the message already resolved', async () => {
    vi.useFakeTimers();
    const port = fakePort();
    const provider = createNativeMessagingProvider({ connect: () => port, hostName: 'h' });

    const promise = provider.complete({ prompt: 'p', timeoutMs: 10 });
    port.emitMessage({ text: 'reply' });
    await vi.advanceTimersByTimeAsync(10);

    await expect(promise).resolves.toBe('reply');
    vi.useRealTimers();
  });

  it('ignores a late message after timeout already rejected', async () => {
    vi.useFakeTimers();
    const port = fakePort();
    const provider = createNativeMessagingProvider({ connect: () => port, hostName: 'h' });

    const promise = provider.complete({ prompt: 'p', timeoutMs: 10 });
    const assertion = expect(promise).rejects.toThrow(/timed out/);
    await vi.advanceTimersByTimeAsync(10);
    port.emitMessage({ text: 'too-late' });
    await assertion;
    vi.useRealTimers();
  });
});
