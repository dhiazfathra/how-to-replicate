export type Step = {
  n: number;
  text: string;
  eventIds: string[];
  tVideo: number | null;
};

export type ReplicationDoc = {
  title: string;
  summary: string;
  steps: Step[];
  expected: string | null;
  actual: string | null;
  generator: 'deterministic' | 'llm';
  generatorModel: string | null;
};
