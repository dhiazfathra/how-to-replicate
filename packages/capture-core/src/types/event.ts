export type ConsolePayload = {
  level: 'log' | 'info' | 'warn' | 'error' | 'debug';
  text: string;
  stack: string | null;
};

export type NetworkPayload = {
  method: string;
  url: string;
  status: number | null;
  requestHeaders: Record<string, string>;
  responseHeaders: Record<string, string>;
  requestBody: string | null;
  responseBody: string | null;
  bodyTruncated: boolean;
  bodyDropped: boolean;
  durationMs: number | null;
  sizeBytes: number | null;
};

export type InteractionPayload = {
  type: 'click' | 'input' | 'keydown' | 'scroll' | 'submit';
  targetName: string;
  targetSelector: string;
  url: string;
  value: string | null;
};

export type NavigationPayload = {
  from: string | null;
  to: string;
  trigger: 'load' | 'pushstate' | 'popstate' | 'replacestate' | 'hashchange';
};

export type LifecyclePayload = {
  transition: string;
  detail: string | null;
};

export type AnnotationPayload = {
  text: string;
};

export type CaptureEvent = {
  id: string;
  captureId: string;
  t: number;
  kind:
    | 'console'
    | 'network'
    | 'interaction'
    | 'navigation'
    | 'lifecycle'
    | 'annotation';
  payload:
    | ConsolePayload
    | NetworkPayload
    | InteractionPayload
    | NavigationPayload
    | LifecyclePayload
    | AnnotationPayload;
  redaction: {
    rulesApplied: string[];
    fidelity: 'full' | 'redacted' | 'dropped';
  };
};
