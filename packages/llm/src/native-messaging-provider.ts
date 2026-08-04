import { providerBrand, type LlmProvider } from './provider.js';

/**
 * Minimal shape of a `chrome.runtime.Port`, as used by native messaging. Kept
 * local so this package never imports `chrome` types directly.
 */
export type NativePort = {
  postMessage: (message: unknown) => void;
  onMessage: { addListener: (callback: (message: unknown) => void) => void };
  onDisconnect: { addListener: (callback: () => void) => void };
  disconnect: () => void;
};

export type NativeMessagingConfig = {
  /** Injected so this package never touches `chrome.runtime` directly. */
  connect: (hostName: string) => NativePort;
  hostName: string;
};

/**
 * Wraps `chrome.runtime.connectNative` as an `LlmProvider`. `target` is
 * always `'native-messaging'` — it never leaves the machine via the network
 * stack, so it is always eligible for `policy.localOnly`.
 */
export function createNativeMessagingProvider(config: NativeMessagingConfig): LlmProvider {
  return {
    name: `native-messaging:${config.hostName}`,
    target: 'native-messaging',
    [providerBrand]: true,
    complete({ prompt, timeoutMs }) {
      return new Promise((resolve, reject) => {
        const port = config.connect(config.hostName);
        let settled = false;

        // `clearTimeout` below guarantees this only ever fires while still
        // unsettled, so no extra guard is needed here.
        const timer = setTimeout(() => {
          settled = true;
          port.disconnect();
          reject(new Error('native-messaging-provider: timed out'));
        }, timeoutMs);

        port.onMessage.addListener((message) => {
          if (settled) return;
          const text = (message as { text?: unknown } | null)?.text;
          settled = true;
          clearTimeout(timer);
          port.disconnect();
          if (typeof text !== 'string') {
            reject(new Error('native-messaging-provider: malformed response'));
            return;
          }
          resolve(text);
        });

        port.onDisconnect.addListener(() => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          reject(new Error('native-messaging-provider: port disconnected'));
        });

        port.postMessage({ prompt });
      });
    },
  };
}
