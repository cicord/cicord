import { useCicord, useMe, usePluginQuery } from "@cicord/react";
import { Button, Notice } from "@cicord/ui";
import { useState } from "react";
import { useT } from "../i18n/index.tsx";
import { SignIn, useServer } from "../portal.tsx";

type Appeal = { case: number; text: string; status: string; resolution?: string };
type CaseView = { case: { number: number; action: string; reason: string }; appeal: Appeal | null };

export function Appeals({ staff = false }: { staff?: boolean }) {
  const t = useT(),
    labels = t.appeals;
  const { client } = useCicord();
  const server = useServer(),
    me = useMe();
  const qs = new URLSearchParams(window.location.search);
  const bot = qs.get("bot"),
    instance = qs.get("instance");
  const choices = server.plugins.filter(
    (p) => p.plugin === "moderation" && p.routes.includes("POST /appeals"),
  );
  const provider =
    choices.find((p) => p.bot === bot && p.instance === instance) ??
    (!bot && !instance ? choices[0] : undefined);
  const at = me.data && provider ? { bot: provider.bot, instance: provider.instance } : undefined;
  const renewal = !staff && qs.get("renew") === "1";
  const view = usePluginQuery<CaseView>(at && !staff && !renewal ? at : undefined, "/appeal");
  const list = usePluginQuery<{ items: Appeal[]; next: string | null }>(
    at && staff ? at : undefined,
    "/appeals",
  );
  const [body, setBody] = useState(""),
    [busy, setBusy] = useState(false),
    [status, setStatus] = useState("");
  async function submit(path: string, input: object) {
    if (!at || busy) return;
    setBusy(true);
    setStatus("");
    try {
      const route = client.plugin(server.key, at.bot, at.instance);
      const result = await route.post<Appeal>(path, input);
      if (renewal) setStatus(labels.sent);
      else {
        setStatus(result.resolution ?? labels.saved);
        if (staff) await list.refetch();
        else await view.refetch();
      }
    } catch {
      setStatus(labels.failed);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="page">
      <header className="page__header">
        <h1>{staff ? labels.review : labels.title}</h1>
      </header>
      {!me.data ? (
        <SignIn />
      ) : !provider ? (
        <Notice tone="info" title={labels.unavailable} />
      ) : renewal ? (
        <Button disabled={busy} onClick={() => void submit("/scope/renew", {})}>
          {labels.renew}
        </Button>
      ) : staff ? (
        list.error ? (
          <Notice tone="error" title={labels.failed} />
        ) : (
          <ul>
            {list.data?.items.map((a) => (
              <li key={a.case}>
                <h2>
                  #{a.case} · {a.status}
                </h2>
                <p>{a.text}</p>
                {a.resolution ? <p>{a.resolution}</p> : null}
                {a.status === "pending" ? (
                  <div>
                    <Button
                      disabled={busy}
                      onClick={() =>
                        void submit("/appeals/" + a.case + "/decision", { decision: "accepted" })
                      }
                    >
                      {labels.accept}
                    </Button>
                    <Button
                      variant="secondary"
                      disabled={busy}
                      onClick={() =>
                        void submit("/appeals/" + a.case + "/decision", { decision: "rejected" })
                      }
                    >
                      {labels.reject}
                    </Button>
                  </div>
                ) : null}
              </li>
            ))}
          </ul>
        )
      ) : view.error ? (
        <Notice tone="error" title={labels.failed} />
      ) : view.data ? (
        <div>
          <h2>
            #{view.data.case.number} · {view.data.case.action}
          </h2>
          <p>{view.data.case.reason}</p>
          {view.data.appeal ? (
            <p>{view.data.appeal.status}</p>
          ) : (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void submit("/appeals", { text: body });
              }}
            >
              <label>
                {labels.statement}
                <textarea
                  required
                  rows={5}
                  maxLength={2000}
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                />
              </label>
              <Button disabled={busy || !body.trim()} type="submit">
                {labels.submit}
              </Button>
            </form>
          )}
        </div>
      ) : null}
      <p role="status">{status}</p>
    </section>
  );
}
