import type { Me, SimState } from "@codi/client";
import { codiKeys, useCodi } from "@codi/react";
import { ChevronRight, Loader2, LogIn } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { UserAvatar } from "../components/common.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Input } from "../components/ui/input.tsx";
import { BrandMark, useAdminConfig } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { describeError } from "../lib/errors.ts";
import { useLocation } from "../router.tsx";

function DiscordGlyph() {
  return (
    <svg viewBox="0 0 24 24" className="size-4" aria-hidden="true" fill="currentColor">
      <path d="M20.3 4.4A19.6 19.6 0 0 0 15.4 3l-.6 1.3a18.2 18.2 0 0 0-5.6 0L8.6 3a19.5 19.5 0 0 0-4.9 1.5C.6 9.1-.3 13.6.1 18.1a19.8 19.8 0 0 0 6 3l1.3-2.1a12.8 12.8 0 0 1-2-1l.5-.4a14 14 0 0 0 12.2 0l.5.4c-.6.4-1.3.7-2 1l1.3 2.1a19.7 19.7 0 0 0 6-3c.5-5.2-.9-9.7-3.6-13.7ZM8 15.4c-1.2 0-2.2-1.1-2.2-2.4S6.8 10.6 8 10.6s2.2 1.1 2.2 2.4-1 2.4-2.2 2.4Zm8 0c-1.2 0-2.2-1.1-2.2-2.4s1-2.4 2.2-2.4 2.2 1.1 2.2 2.4-1 2.4-2.2 2.4Z" />
    </svg>
  );
}

export function LoginPage() {
  const { t } = useTranslation();
  const { client, cache } = useCodi();
  const config = useAdminConfig();
  const { search } = useLocation();
  const [sim, setSim] = useState<SimState | null | undefined>(undefined);
  const [userId, setUserId] = useState("");
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<{ message: string; hint?: string | undefined } | null>(null);
  const autoLogin = useRef(false);

  useEffect(() => {
    const ctrl = new AbortController();
    client.sim
      .state({ signal: ctrl.signal })
      .then((s) => setSim(s))
      .catch(() => setSim(null));
    return () => ctrl.abort();
  }, [client]);

  const login = async (id: string) => {
    setPending(id);
    setError(null);
    try {
      const me: Me = await client.auth.devLogin(id);
      cache.setData(codiKeys.me(), me);
      void cache.invalidate(["admin"]);
    } catch (e) {
      const d = describeError(e);
      setError({ message: d.message, hint: d.hint });
      setPending(null);
    }
  };

  // Mock mode: "Continue with Discord" comes back here; sign in as the server owner.
  useEffect(() => {
    if (config.mock && search.get("mock-oauth") && sim && !autoLogin.current) {
      autoLogin.current = true;
      const owner = sim.servers[0]?.ownerId;
      if (owner) void login(owner);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [config.mock, search, sim]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (userId.trim()) void login(userId.trim());
  };

  const owners = new Set((sim?.servers ?? []).map((s) => s.ownerId).filter(Boolean));
  const users = [...(sim?.users ?? [])].sort(
    (a, b) => Number(owners.has(b.id)) - Number(owners.has(a.id)),
  );
  const memberOf = (id: string) =>
    (sim?.servers ?? []).filter((s) => s.members.some((m) => m.user.id === id)).map((s) => s.name);

  return (
    <div className="grid min-h-svh place-items-center bg-background px-4 py-10">
      <div className="w-full max-w-[400px]">
        <div className="mb-8 flex items-center gap-2">
          <BrandMark size={24} />
          <span className="text-[17px] font-semibold tracking-tight">codi</span>
          <span className="text-[15px] text-subtle-foreground">{t("app.admin")}</span>
        </div>
        <h1 className="text-xl font-semibold tracking-[-0.01em]">{t("login.title")}</h1>
        <p className="mt-2 text-[13px] text-muted-foreground">{t("login.text")}</p>
        <Button asChild variant="discord" size="lg" className="mt-6 w-full">
          <a href={config.loginUrl}>
            <DiscordGlyph />
            {t("login.discord")}
          </a>
        </Button>

        {sim === undefined ? (
          <div className="mt-6 flex justify-center">
            <Loader2 className="size-4 animate-spin text-muted-foreground" aria-hidden="true" />
          </div>
        ) : sim ? (
          <section className="mt-8 rounded-xl border bg-card" aria-labelledby="dev-login-title">
            <div className="flex items-center justify-between border-b px-4 py-3">
              <div>
                <h2 id="dev-login-title" className="text-[13px] font-semibold">
                  {t("login.dev.title")}
                </h2>
                <p className="text-xs text-muted-foreground">{t("login.dev.text")}</p>
              </div>
              <Badge tone="violet">{t("login.dev.simulator")}</Badge>
            </div>
            <ul className="max-h-72 divide-y overflow-y-auto">
              {users.map((u) => {
                const servers = memberOf(u.id);
                const owner = owners.has(u.id);
                return (
                  <li key={u.id}>
                    <button
                      className="flex w-full items-center gap-3 px-4 py-2.5 text-left outline-none hover:bg-surface-hover focus-visible:bg-surface-hover disabled:opacity-60"
                      onClick={() => void login(u.id)}
                      disabled={pending !== null}
                    >
                      <UserAvatar name={u.displayName || u.username} url={u.avatarUrl} size={28} />
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-1.5 text-[13px] font-medium">
                          {u.displayName || u.username}
                          {owner ? <Badge tone="brand">{t("login.dev.owner")}</Badge> : null}
                        </span>
                        <span className="block truncate text-xs text-muted-foreground">
                          @{u.username} ·{" "}
                          {servers.length
                            ? t("login.dev.memberOf", { servers: servers.join(", ") })
                            : t("login.dev.notMember")}
                        </span>
                      </span>
                      {pending === u.id ? (
                        <Loader2
                          className="size-4 animate-spin text-muted-foreground"
                          aria-hidden="true"
                        />
                      ) : (
                        <ChevronRight
                          className="size-4 text-subtle-foreground"
                          aria-hidden="true"
                        />
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
            <div className="border-t p-3">
              <DevIdForm
                userId={userId}
                setUserId={setUserId}
                submit={submit}
                pending={pending !== null}
              />
            </div>
          </section>
        ) : null}
        {error ? (
          <div
            role="alert"
            className="mt-4 rounded-lg border border-danger/30 bg-danger-soft px-3 py-2 text-[13px]"
          >
            <p className="font-medium">{error.message}</p>
            {error.hint ? <p className="mt-0.5 text-muted-foreground">{error.hint}</p> : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function DevIdForm(p: {
  userId: string;
  setUserId: (v: string) => void;
  submit: (e: FormEvent) => void;
  pending: boolean;
}) {
  const { t } = useTranslation();
  return (
    <form className="flex gap-2" onSubmit={p.submit}>
      <label htmlFor="dev-user-id" className="sr-only">
        {t("login.dev.userId")}
      </label>
      <Input
        id="dev-user-id"
        className="font-mono text-[13px]"
        placeholder={t("login.dev.userIdPlaceholder")}
        value={p.userId}
        onChange={(e) => p.setUserId(e.target.value)}
        autoComplete="off"
        spellCheck={false}
      />
      <Button type="submit" variant="outline" disabled={!p.userId.trim()} loading={p.pending}>
        {p.pending ? null : <LogIn />}
        {t("login.dev.signIn")}
      </Button>
    </form>
  );
}
