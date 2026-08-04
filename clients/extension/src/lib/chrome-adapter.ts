/**
 * Structural subset of the Chrome extension API surface this extension
 * touches. Deliberately not `@types/chrome` (no new dependency, and a
 * hand-rolled structural type doubles as the seam that lets every background/
 * content module take its Chrome surface through an injected adapter instead
 * of the ambient `chrome` global — the brief's testing requirement).
 *
 * Every member is a property-typed function (`foo: (x) => y`), not a method
 * signature (`foo(x): y`) — method signatures are implicitly `this`-bound,
 * which trips `@typescript-eslint/unbound-method` the moment a test passes
 * `dbg.attach` to `expect(...).toHaveBeenCalledWith` instead of calling it.
 */

export type DebuggerTarget = { tabId: number };

export type DebuggerEvent = {
  method: string;
  params: Record<string, unknown>;
};

export type ChromeDebugger = {
  attach: (target: DebuggerTarget, version: string) => Promise<void>;
  detach: (target: DebuggerTarget) => Promise<void>;
  sendCommand: (
    target: DebuggerTarget,
    method: string,
    params?: Record<string, unknown>,
  ) => Promise<unknown>;
  onEvent: ChromeEvent<(source: DebuggerTarget, message: DebuggerEvent) => void>;
  onDetach: ChromeEvent<(source: DebuggerTarget, reason: string) => void>;
};

export type WebRequestDetails = {
  requestId: string;
  url: string;
  method: string;
  tabId: number;
  statusCode?: number;
  responseHeaders?: { name: string; value?: string }[];
};

export type ChromeWebRequest = {
  onBeforeRequest: ChromeEvent<(details: WebRequestDetails) => void>;
  onCompleted: ChromeEvent<(details: WebRequestDetails) => void>;
};

export type ChromeEvent<Listener> = {
  addListener: (listener: Listener) => void;
  removeListener: (listener: Listener) => void;
};

export type ChromeStorageArea = {
  get: (keys: string | string[] | null) => Promise<Record<string, unknown>>;
};

export type ChromeStorage = {
  managed: ChromeStorageArea;
  onChanged: ChromeEvent<(changes: Record<string, unknown>, areaName: string) => void>;
};

export type ChromeOffscreen = {
  hasDocument: () => Promise<boolean>;
  createDocument: (options: {
    url: string;
    reasons: string[];
    justification: string;
  }) => Promise<void>;
  closeDocument: () => Promise<void>;
};

export type ChromeRuntime = {
  sendMessage: (message: unknown) => Promise<unknown>;
  /**
   * A listener returns `true` to keep the message channel open for an
   * asynchronous `sendResponse` call (used by the offscreen document's
   * screenshot-capture request, which the service worker answers after
   * awaiting `chrome.tabs.captureVisibleTab()`).
   */
  onMessage: ChromeEvent<(message: unknown, sender: unknown, sendResponse: (r?: unknown) => void) => boolean | void>;
  getURL: (path: string) => string;
};

export type ChromeAction = {
  setBadgeText: (details: { text: string; tabId?: number }) => Promise<void>;
};

/** `captureVisibleTab` is the periodic-screenshot floor for degraded captures (invariant 2). */
export type ChromeTabs = {
  captureVisibleTab: () => Promise<string>;
};

export type ChromeAdapter = {
  debugger: ChromeDebugger;
  webRequest: ChromeWebRequest;
  storage: ChromeStorage;
  offscreen: ChromeOffscreen;
  runtime: ChromeRuntime;
  action: ChromeAction;
  tabs: ChromeTabs;
};
