import { createCicordClient, type AdminEvent, type EventStreamOptions } from "@cicord/client";
import { CicordProvider, QueryCache } from "@cicord/react";
import { act, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vite-plus/test";
import { useAdminLive } from "../src/shell/live.ts";

function Probe() {
  const live = useAdminLive(true);
  return (
    <p>
      {live.connection} {live.updatedTo ?? "-"}
    </p>
  );
}

function setup() {
  const client = createCicordClient({ fetch: async () => new Response("{}") });
  let emit: (e: AdminEvent) => void = () => {};
  let opts: Omit<EventStreamOptions, "onEvent"> = {};
  const close = vi.fn();
  const subscribe = vi
    .spyOn(client.admin, "subscribe")
    .mockImplementation((onEvent, options = {}) => {
      emit = onEvent;
      opts = options;
      return { close, closed: Promise.resolve() };
    });
  const cache = new QueryCache();
  const invalidate = vi.spyOn(cache, "invalidate");
  render(
    <CicordProvider client={client} cache={cache}>
      <Probe />
    </CicordProvider>,
  );
  return { emit: (e: AdminEvent) => emit(e), opts: () => opts, invalidate, close, subscribe };
}

describe("useAdminLive", () => {
  test("refreshes on hints, reports a new runtime version and a lost connection", async () => {
    vi.useFakeTimers();
    try {
      const s = setup();
      expect(screen.getByText(/connecting/)).toBeTruthy();
      act(() => {
        s.opts().onOpen?.();
        s.emit({ type: "hello", version: "0.1.3", revision: "a" });
      });
      expect(screen.getByText("live -")).toBeTruthy();

      act(() => {
        s.emit({ type: "analytics", server: "main" });
        s.emit({ type: "analytics", server: "main" });
        vi.advanceTimersByTime(400);
      });
      expect(s.invalidate).toHaveBeenCalledTimes(1); // coalesced
      expect(s.invalidate).toHaveBeenCalledWith(["admin", "server", "main", "analytics"]);

      // The runtime restarts: the stream drops, then comes back with another version.
      act(() => {
        s.opts().onError?.(new Error("event stream: disconnected"));
        vi.advanceTimersByTime(1000);
      });
      expect(screen.getByText("live -")).toBeTruthy(); // within the grace period
      act(() => {
        vi.advanceTimersByTime(4000);
      });
      expect(screen.getByText("reconnecting -")).toBeTruthy();
      act(() => {
        s.opts().onOpen?.();
        s.emit({ type: "hello", version: "0.1.4", revision: "b" });
        vi.advanceTimersByTime(400);
      });
      expect(screen.getByText("live 0.1.4")).toBeTruthy();
      expect(s.invalidate).toHaveBeenLastCalledWith(["admin"]); // missed changes

      // The runtime announces a restart: a new connection shortly after.
      act(() => {
        s.emit({ type: "stopping" });
      });
      expect(s.close).toHaveBeenCalledTimes(1);
      act(() => {
        vi.advanceTimersByTime(2100);
      });
      expect(s.subscribe).toHaveBeenCalledTimes(2);

      // Signed out: the stream stops instead of retrying.
      act(() => s.opts().onError?.(new Error("event stream /events: HTTP 401")));
      expect(s.close).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
