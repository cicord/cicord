import { createCodiClient, memoryTokenStore, type FetchLike } from "@codi/client";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, test } from "vite-plus/test";
import { CodiProvider, useLinkTokenLogin, useMe, useSession } from "../src/index.ts";

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const me = {
  user: { id: "1", username: "alice", displayName: "Alice", avatarUrl: "" },
  servers: [],
};

test("token mode: codi_code login, session hook, logout on 401", async () => {
  let revoked = false;
  const fetch: FetchLike = async (url) => {
    if (url.endsWith("/auth/token"))
      return json({
        token: "codi_st_x",
        expiresAt: new Date(Date.now() + 60_000).toISOString(),
        me,
      });
    if (revoked) return json({ error: { code: "unauthenticated", message: "revoked" } }, 401);
    return json(me);
  };
  const client = createCodiClient({
    baseUrl: "https://codi.example",
    fetch,
    auth: "token",
    tokenStore: memoryTokenStore(),
  });
  window.history.replaceState(null, "", "/cb?codi_code=abc");
  const wrapper = ({ children }: { children: ReactNode }) => (
    <CodiProvider client={client}>{children}</CodiProvider>
  );
  const { result } = renderHook(
    () => ({ login: useLinkTokenLogin(), session: useSession(), me: useMe() }),
    { wrapper },
  );
  await waitFor(() => expect(result.current.login.status).toBe("success"));
  expect(window.location.search).toBe("");
  expect(result.current.session?.token).toBe("codi_st_x");
  expect(result.current.me.data?.user.id).toBe("1");

  // The token is revoked server side: the next call logs the viewer out.
  revoked = true;
  await result.current.me.refetch();
  await waitFor(() => expect(result.current.session).toBeNull());
  await waitFor(() => expect(result.current.me.data).toBeNull());
});
