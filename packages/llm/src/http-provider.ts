import type { LlmProvider, ProviderTarget } from './provider.js';

export type HttpProviderConfig = {
  baseUrl: string;
  apiKey?: string;
  model: string;
  fetch?: typeof fetch;
};

/**
 * True only for a real loopback host: `localhost`, an IPv4 literal in
 * `127.0.0.0/8`, or the IPv6 loopback `::1`. Matches on the parsed hostname
 * only — a name that merely *looks* local (`localhost.example.com`, or one
 * that happens to resolve to loopback via DNS) is deliberately `'remote'`;
 * resolving DNS here would make this async and still be spoofable.
 */
function isLoopbackHost(hostname: string): boolean {
  if (hostname === 'localhost' || hostname === '::1' || hostname === '[::1]') return true;
  const octets = hostname.split('.');
  return octets.length === 4 && octets[0] === '127' && octets.every((o) => /^\d+$/.test(o));
}

function deriveTarget(baseUrl: string): ProviderTarget {
  try {
    const { hostname } = new URL(baseUrl);
    return isLoopbackHost(hostname) ? 'localhost' : 'remote';
  } catch {
    return 'remote';
  }
}

type ChatCompletionResponse = {
  choices?: Array<{ message?: { content?: unknown } }>;
};

/**
 * Posts an OpenAI-compatible `/chat/completions` request. `fetch` is
 * injectable so the package needs no runtime dependency and stays testable.
 */
export function createHttpProvider(config: HttpProviderConfig): LlmProvider {
  const doFetch = config.fetch ?? fetch;
  const url = `${config.baseUrl.replace(/\/+$/, '')}/chat/completions`;

  return {
    name: `http:${config.model}`,
    target: deriveTarget(config.baseUrl),
    async complete({ prompt, timeoutMs }) {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), timeoutMs);
      try {
        const res = await doFetch(url, {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            ...(config.apiKey ? { authorization: `Bearer ${config.apiKey}` } : {}),
          },
          body: JSON.stringify({
            model: config.model,
            messages: [{ role: 'user', content: prompt }],
          }),
          signal: controller.signal,
        });

        if (!res.ok) {
          throw new Error(`http-provider: request failed with status ${res.status}`);
        }

        const json = (await res.json()) as ChatCompletionResponse;
        const content = json.choices?.[0]?.message?.content;
        if (typeof content !== 'string') {
          throw new Error('http-provider: malformed response, missing choices[0].message.content');
        }
        return content;
      } finally {
        clearTimeout(timer);
      }
    },
  };
}
