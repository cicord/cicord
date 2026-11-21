import type { Me, SimState } from "@codi/client";
import { codiKeys, useCodi } from "@codi/react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { BrandMark, useAdminConfig } from "../context.tsx";
import { useLocation } from "../router.tsx";
import { IconDiscord, IconLogin } from "../ui/icons.tsx";
import { Avatar, Badge, Button, errorMessage, Spinner } from "../ui/index.tsx";

export function LoginPage() {
  const { client, cache } = useCodi();
  const config = useAdminConfig();
  const { search } = useLocation();
  const [sim, setSim] = useState<SimState | null | undefined>(undefined);
  const [showDev, setShowDev] = useState(false);
  const [userId, setUserId] = useState("");
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
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
      setError(
        e instanceof Error && "status" in e && (e as { status: number }).status === 404
          ? `Dev login failed: ${errorMessage(e)}. It only works with \`codi dev\`.`
          : errorMessage(e),
      );
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
    <div className="login">
      <div className="login-card">
        <div className="login-brand">
          <BrandMark size={28} />
          <span className="brand-name">codi</span>
          <span className="brand-sub">admin</span>
        </div>
        <h1 className="login-title">Sign in to the operator console</h1>
        <p className="login-text">
          Manage the servers, bots and plugins this runtime reconciles from git. Access is granted
          by the <code>spec.access</code> section of each server.
        </p>
        <a className="btn btn-discord btn-md login-discord" href={config.loginUrl}>
          <IconDiscord size={18} />
          <span>Continue with Discord</span>
        </a>

        {sim === undefined ? (
          <div className="login-dev-loading">
            <Spinner size={14} />
          </div>
        ) : sim ? (
          <section className="login-dev" aria-labelledby="dev-login-title">
            <div className="login-dev-head">
              <h2 id="dev-login-title">Dev login</h2>
              <Badge tone="violet">Simulator</Badge>
            </div>
            <p className="login-dev-text">
              Pick a simulated user. The server owner is an admin everywhere.
            </p>
            <ul className="login-users">
              {users.map((u) => {
                const servers = memberOf(u.id);
                return (
                  <li key={u.id}>
                    <button
                      className="login-user"
                      onClick={() => void login(u.id)}
                      disabled={pending !== null}
                    >
                      <Avatar name={u.displayName || u.username} url={u.avatarUrl} size={28} />
                      <span className="login-user-main">
                        <span className="login-user-name">
                          {u.displayName || u.username}
                          {owners.has(u.id) ? <Badge tone="accent">Owner</Badge> : null}
                        </span>
                        <span className="login-user-meta">
                          @{u.username} ·{" "}
                          {servers.length ? `member of ${servers.join(", ")}` : "not a member yet"}
                        </span>
                      </span>
                      {pending === u.id ? <Spinner size={14} /> : <IconLogin size={14} />}
                    </button>
                  </li>
                );
              })}
            </ul>
            <DevIdForm
              userId={userId}
              setUserId={setUserId}
              submit={submit}
              pending={pending !== null}
            />
          </section>
        ) : (
          <div className="login-dev-collapsed">
            {showDev ? (
              <section className="login-dev" aria-label="Dev login">
                <p className="login-dev-text">
                  Available with <code>codi dev</code> only: sign in as any Discord user ID.
                </p>
                <DevIdForm
                  userId={userId}
                  setUserId={setUserId}
                  submit={submit}
                  pending={pending !== null}
                />
              </section>
            ) : (
              <button className="link-btn" onClick={() => setShowDev(true)}>
                Use dev login
              </button>
            )}
          </div>
        )}
        {error ? (
          <p className="login-error" role="alert">
            {error}
          </p>
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
  return (
    <form className="login-id" onSubmit={p.submit}>
      <label htmlFor="dev-user-id" className="sr-only">
        User ID
      </label>
      <input
        id="dev-user-id"
        className="input mono"
        placeholder="User ID, e.g. 1122334455"
        value={p.userId}
        onChange={(e) => p.setUserId(e.target.value)}
        autoComplete="off"
        spellCheck={false}
      />
      <Button type="submit" disabled={!p.userId.trim()} loading={p.pending}>
        Sign in
      </Button>
    </form>
  );
}
