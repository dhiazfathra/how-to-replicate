export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

export type AssetRef = {
  id: string;
  captureId: string;
  kind: 'video' | 'screenshot' | 'har';
  mimeType: string;
  sizeBytes: number;
  chunkCount: number;
  sha256: string;
};

export type EnvSnapshot = {
  userAgent: string;
  platform: string;
  viewport: { w: number; h: number };
  devicePixelRatio: number;
  locale: string;
  timezone: string;
  url: string;
};
