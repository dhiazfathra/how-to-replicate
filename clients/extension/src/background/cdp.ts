import { newId, type CaptureEvent, type ConsolePayload, type NetworkPayload } from '@htr/capture-core';
import type { Clock } from '@htr/capture-core';
import type { ChromeDebugger, DebuggerEvent, DebuggerTarget } from '../lib/chrome-adapter.js';

const CDP_VERSION = '1.3';
const NETWORK_ENABLE = 'Network.enable';
const CONSOLE_ENABLE = 'Console.enable';
const RUNTIME_ENABLE = 'Runtime.enable';

export type CdpSession = {
  detach(): Promise<void>;
};

/**
 * Attach `chrome.debugger` to `target`, enable Network/Console/Runtime, and
 * forward every mapped event to `onEvent`. Pending Network request metadata
 * (headers, method, url) is tracked in-memory keyed by CDP `requestId` so the
 * eventual `Network.loadingFinished`/`responseReceived` pair can be folded
 * into one `NetworkPayload` — CDP reports a request over 2+ events, but
 * capture-core's `CaptureEvent` models one network call as one event.
 */
export async function attachCdp(
  chromeDebugger: ChromeDebugger,
  target: DebuggerTarget,
  captureId: string,
  clock: Clock,
  onEvent: (event: CaptureEvent) => void,
): Promise<CdpSession> {
  await chromeDebugger.attach(target, CDP_VERSION);
  await chromeDebugger.sendCommand(target, NETWORK_ENABLE);
  await chromeDebugger.sendCommand(target, CONSOLE_ENABLE);
  await chromeDebugger.sendCommand(target, RUNTIME_ENABLE);

  const pending = new Map<string, PendingRequest>();

  const listener = (source: DebuggerTarget, message: DebuggerEvent): void => {
    if (source.tabId !== target.tabId) return;
    const mapped = mapCdpEvent(message, captureId, clock, pending);
    if (mapped) onEvent(mapped);
  };

  chromeDebugger.onEvent.addListener(listener);

  return {
    async detach(): Promise<void> {
      chromeDebugger.onEvent.removeListener(listener);
      await chromeDebugger.detach(target);
    },
  };
}

function headerArrayToRecord(headers: unknown): Record<string, string> {
  if (typeof headers !== 'object' || headers === null) return {};
  return headers as Record<string, string>;
}

type PendingRequest = {
  url: string;
  method: string;
  requestHeaders: Record<string, string>;
  requestBody: string | null;
  status?: number;
  responseHeaders?: Record<string, string>;
};

export function mapCdpEvent(
  message: DebuggerEvent,
  captureId: string,
  clock: Clock,
  pending: Map<string, PendingRequest>,
): CaptureEvent | null {
  const { method, params } = message;

  switch (method) {
    case 'Network.requestWillBeSent': {
      const requestId = params.requestId as string;
      const request = params.request as {
        url: string;
        method: string;
        headers: Record<string, string>;
        postData?: string;
      };
      pending.set(requestId, {
        url: request.url,
        method: request.method,
        requestHeaders: headerArrayToRecord(request.headers),
        requestBody: request.postData ?? null,
      });
      return null;
    }

    case 'Network.responseReceived': {
      const requestId = params.requestId as string;
      const response = params.response as { status: number; headers: Record<string, string> };
      const entry = pending.get(requestId);
      if (!entry) return null;
      entry.status = response.status;
      entry.responseHeaders = headerArrayToRecord(response.headers);
      return null;
    }

    case 'Network.loadingFinished': {
      const requestId = params.requestId as string;
      const entry = pending.get(requestId);
      pending.delete(requestId);
      if (!entry) return null;
      const payload: NetworkPayload = {
        method: entry.method,
        url: entry.url,
        status: entry.status ?? null,
        requestHeaders: entry.requestHeaders,
        responseHeaders: entry.responseHeaders ?? {},
        requestBody: entry.requestBody ?? null,
        responseBody: null,
        bodyTruncated: false,
        bodyDropped: false,
        durationMs: null,
        sizeBytes: typeof params.encodedDataLength === 'number' ? params.encodedDataLength : null,
      };
      return buildEvent(captureId, clock, 'network', payload);
    }

    case 'Console.messageAdded': {
      const consoleMessage = params.message as { level: string; text: string; stackTrace?: unknown };
      const payload: ConsolePayload = {
        level: normalizeConsoleLevel(consoleMessage.level),
        text: consoleMessage.text,
        stack: consoleMessage.stackTrace ? JSON.stringify(consoleMessage.stackTrace) : null,
      };
      return buildEvent(captureId, clock, 'console', payload);
    }

    case 'Runtime.exceptionThrown': {
      const exceptionDetails = params.exceptionDetails as {
        text: string;
        exception?: { description?: string };
        stackTrace?: unknown;
      };
      const payload: ConsolePayload = {
        level: 'error',
        text: exceptionDetails.exception?.description ?? exceptionDetails.text,
        stack: exceptionDetails.stackTrace ? JSON.stringify(exceptionDetails.stackTrace) : null,
      };
      return buildEvent(captureId, clock, 'console', payload);
    }

    default:
      return null;
  }
}

function normalizeConsoleLevel(level: string): ConsolePayload['level'] {
  switch (level) {
    case 'log':
    case 'info':
    case 'warn':
    case 'error':
    case 'debug':
      return level;
    default:
      return 'log';
  }
}

function buildEvent(
  captureId: string,
  clock: Clock,
  kind: 'network' | 'console',
  payload: NetworkPayload | ConsolePayload,
): CaptureEvent {
  return {
    id: newId(),
    captureId,
    t: clock.now(),
    kind,
    payload,
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}
