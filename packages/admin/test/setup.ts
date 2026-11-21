// jsdom gaps that Radix UI, cmdk and the theme / mobile hooks rely on.
import { cleanup } from "@testing-library/react";
import { afterEach } from "vite-plus/test";

afterEach(cleanup);

if (!window.matchMedia) {
  window.matchMedia = (query: string): MediaQueryList =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    }) as MediaQueryList;
}

class NoopResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= NoopResizeObserver as unknown as typeof ResizeObserver;

const proto = window.HTMLElement.prototype as unknown as Record<string, unknown>;
proto["scrollIntoView"] ??= () => undefined;
proto["hasPointerCapture"] ??= () => false;
proto["releasePointerCapture"] ??= () => undefined;
proto["setPointerCapture"] ??= () => undefined;

window.scrollTo = (() => undefined) as typeof window.scrollTo;
