/**
 * Minimal Server-Sent Events client built on fetch (not EventSource), so it can send an
 * `Authorization` header, works in Node 24 and is easy to test with a mocked fetch.
 */

export interface ServerSentEvent {
  /** Event type (`event:` field), "message" by default. */
  event: string;
  data: string;
  id: string;
}

export interface EventStreamOptions {
  onEvent: (event: ServerSentEvent) => void;
  /** Called on connection errors; the stream reconnects afterwards unless closed. */
  onError?: (error: unknown) => void;
  /** Called when a connection is (re)established. */
  onOpen?: () => void;
  /** Delay before reconnecting, in ms (updated by the server's `retry:` field). Default 2000. */
  retryMs?: number;
  /** Stop after the first disconnection instead of reconnecting. */
  once?: boolean;
  signal?: AbortSignal;
}

export interface EventStream {
  close: () => void;
  /** Resolves when the stream is closed for good. */
  readonly closed: Promise<void>;
}

export type StreamFetch = (url: string, init: RequestInit) => Promise<Response>;

/** Incremental SSE parser (https://html.spec.whatwg.org/multipage/server-sent-events.html). */
export class SseParser {
  #buffer = "";
  #data: string[] = [];
  #event = "";
  #lastId = "";
  retry: number | undefined;
  readonly #emit: (event: ServerSentEvent) => void;

  constructor(emit: (event: ServerSentEvent) => void) {
    this.#emit = emit;
  }

  push(chunk: string): void {
    this.#buffer += chunk;
    let index: number;
    while ((index = this.#buffer.search(/\r\n|\r|\n/)) >= 0) {
      const line = this.#buffer.slice(0, index);
      const sep = this.#buffer.startsWith("\r\n", index) ? 2 : 1;
      // A lone trailing "\r" might be the first half of "\r\n": wait for more input.
      if (sep === 1 && this.#buffer[index] === "\r" && index === this.#buffer.length - 1) break;
      this.#buffer = this.#buffer.slice(index + sep);
      this.#line(line);
    }
  }

  #line(line: string): void {
    if (line === "") {
      if (this.#data.length > 0) {
        this.#emit({
          event: this.#event || "message",
          data: this.#data.join("\n"),
          id: this.#lastId,
        });
      }
      this.#data = [];
      this.#event = "";
      return;
    }
    if (line.startsWith(":")) return;
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    switch (field) {
      case "data":
        this.#data.push(value);
        break;
      case "event":
        this.#event = value;
        break;
      case "id":
        if (!value.includes("\0")) this.#lastId = value;
        break;
      case "retry":
        if (/^\d+$/.test(value)) this.retry = Number(value);
        break;
    }
  }

  get lastEventId(): string {
    return this.#lastId;
  }
}

export function openEventStream(
  fetcher: StreamFetch,
  url: string,
  init: RequestInit,
  options: EventStreamOptions,
): EventStream {
  const controller = new AbortController();
  const abort = (): void => controller.abort();
  options.signal?.addEventListener("abort", abort, { once: true });
  if (options.signal?.aborted) controller.abort();
  let retryMs = options.retryMs ?? 2000;
  let lastEventId = "";

  const run = async (): Promise<void> => {
    while (!controller.signal.aborted) {
      const parser = new SseParser(options.onEvent);
      try {
        const headers = new Headers(init.headers);
        headers.set("Accept", "text/event-stream");
        if (lastEventId) headers.set("Last-Event-ID", lastEventId);
        const response = await fetcher(url, { ...init, headers, signal: controller.signal });
        if (!response.ok || !response.body) {
          throw new Error(`event stream ${url}: HTTP ${response.status}`);
        }
        options.onOpen?.();
        const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          parser.push(value);
          lastEventId = parser.lastEventId;
          if (parser.retry !== undefined) retryMs = parser.retry;
        }
        if (!controller.signal.aborted)
          options.onError?.(new Error(`event stream ${url}: disconnected`));
      } catch (error) {
        if (controller.signal.aborted) break;
        options.onError?.(error);
      }
      if (options.once) break;
      await sleep(retryMs, controller.signal);
    }
    options.signal?.removeEventListener("abort", abort);
  };

  const closed = run();
  return {
    close: abort,
    closed,
  };
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve();
    const timer = setTimeout(done, ms);
    function done(): void {
      clearTimeout(timer);
      signal.removeEventListener("abort", done);
      resolve();
    }
    signal.addEventListener("abort", done, { once: true });
  });
}
