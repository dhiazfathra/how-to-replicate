import { newId, type CaptureEvent, type NetworkPayload } from '@htr/capture-core';
import type { Clock } from '@htr/capture-core';
import type { ChromeAction, ChromeWebRequest, DebuggerTarget, WebRequestDetails } from '../lib/chrome-adapter.js';

export type FallbackSession = {
  stop(): void;
};

/**
 * The DevTools-opened case: Chrome only allows one `chrome.debugger` client
 * per tab, so opening DevTools forces a detach we didn't ask for. Losing
 * Console/Runtime visibility silently would present a confident-looking
 * document built on a hole — so this degrades loudly instead: fidelity flips
 * to `degraded`, a `lifecycle` event records the handoff, and a badge marks
 * the tab so it's visible outside the capture doc too (invariant 4).
 *
 * `webRequest` replaces the CDP `Network` domain (headers + status only — no
 * body access from this API, which `bodyDropped` reflects). There is no
 * `webRequest` equivalent for `Console`/`Runtime`; console monkey-patching
 * happens in the content script's world instead (out of this module's
 * background-context reach), which is why the returned lifecycle event
 * calls the handoff out explicitly rather than pretending parity.
 */
export function startFallback(
  webRequest: ChromeWebRequest,
  action: ChromeAction,
  target: DebuggerTarget,
  captureId: string,
  clock: Clock,
  onEvent: (event: CaptureEvent) => void,
): FallbackSession {
  const pending = new Map<string, NetworkPayload>();

  const onBeforeRequest = (details: WebRequestDetails): void => {
    if (details.tabId !== target.tabId) return;
    pending.set(details.requestId, {
      method: details.method,
      url: details.url,
      status: null,
      requestHeaders: {},
      responseHeaders: {},
      requestBody: null,
      responseBody: null,
      bodyTruncated: false,
      bodyDropped: true,
      durationMs: null,
      sizeBytes: null,
    });
  };

  const onCompleted = (details: WebRequestDetails): void => {
    if (details.tabId !== target.tabId) return;
    const entry = pending.get(details.requestId);
    pending.delete(details.requestId);
    if (!entry) return;
    const responseHeaders: Record<string, string> = {};
    for (const header of details.responseHeaders ?? []) {
      if (header.value !== undefined) responseHeaders[header.name] = header.value;
    }
    const payload: NetworkPayload = {
      ...entry,
      status: details.statusCode ?? null,
      responseHeaders,
    };
    onEvent({
      id: newId(),
      captureId,
      t: clock.now(),
      kind: 'network',
      payload,
      redaction: { rulesApplied: [], fidelity: 'full' },
    });
  };

  webRequest.onBeforeRequest.addListener(onBeforeRequest);
  webRequest.onCompleted.addListener(onCompleted);
  void action.setBadgeText({ text: 'DEG', tabId: target.tabId });

  return {
    stop(): void {
      webRequest.onBeforeRequest.removeListener(onBeforeRequest);
      webRequest.onCompleted.removeListener(onCompleted);
    },
  };
}

/**
 * Build the `lifecycle` event recording a CDP→fallback handoff. Callers
 * apply this to the capture via `capture-core`'s `transition`/store before
 * appending it — this module only shapes the event, it doesn't mutate state.
 */
export function buildDegradedHandoffEvent(captureId: string, clock: Clock, reason: string): CaptureEvent {
  return {
    id: newId(),
    captureId,
    t: clock.now(),
    kind: 'lifecycle',
    payload: { transition: 'cdp-detach->fallback', detail: reason },
    redaction: { rulesApplied: [], fidelity: 'full' },
  };
}
