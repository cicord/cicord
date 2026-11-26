/** Diagnostics: the setup wizard's live checklist, re-runnable at any time. */
import { useState } from "react";
import type { DeadTask } from "@cicord/client";
import { useCicord } from "@cicord/react";
import { RotateCw } from "lucide-react";
import { Page, PageHeader, Time } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { useTranslation } from "../i18n/index.ts";
import { diagnostics } from "../setup/api.ts";
import { useAction, usePoll } from "../setup/hooks.ts";
import { Checklist, SetupError } from "../setup/kit.tsx";

export function DiagnosticsPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const { client } = useCicord();
  const rep = usePoll((signal) => diagnostics(client, serverKey, signal), 15_000);
  return (
    <Page className="max-w-3xl">
      <PageHeader
        title={t("diagnostics.title")}
        description={t("diagnostics.description")}
        actions={
          <Button variant="outline" size="sm" onClick={rep.refresh}>
            <RotateCw />
            {t("diagnostics.run")}
          </Button>
        }
      />
      <Interop key={`interop-${serverKey}`} serverKey={serverKey} />
      <FailedTasks key={serverKey} serverKey={serverKey} />
      {rep.error ? <SetupError error={rep.error} /> : null}
      {rep.data ? (
        <>
          <p className="mb-2 text-xs text-muted-foreground">
            {t("diagnostics.ranAt")} <Time iso={rep.data.ranAt} /> · {t("diagnostics.sinceStart")}
          </p>
          <Checklist checks={rep.data.checks} label={t("diagnostics.title")} />
        </>
      ) : null}
    </Page>
  );
}

function FailedTasks({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const { client } = useCicord();
  const list = usePoll((signal) => client.admin.deadLetters(serverKey, {}, { signal }), 15_000);
  const action = useAction();
  const [payload, setPayload] = useState<{ id: string; value: string } | null>(null);
  const [discard, setDiscard] = useState<string | null>(null);
  // Pages loaded with "Load more", after the polled first page. Keyset cursors keep them
  // valid while letters are resolved; a resolved letter is dropped locally.
  const [more, setMore] = useState<{ items: DeadTask[]; next: string | null } | null>(null);
  function decide(id: string, decision: "retry" | "discard") {
    void action.run(async () => {
      await client.admin.resolveDeadLetter(serverKey, id, decision);
      setDiscard(null);
      setMore((m) => (m ? { ...m, items: m.items.filter((task) => task.id !== id) } : m));
      list.refresh();
    });
  }
  if (list.error) return <SetupError error={list.error} />;
  if (!list.data?.items.length) return null;
  const next = (more ? more.next : list.data.nextCursor) ?? null;
  const partial = next !== null || (!more && list.data.truncated);
  const items = [...list.data.items];
  for (const task of more?.items ?? []) {
    if (!items.some((shown) => shown.id === task.id)) items.push(task);
  }
  function loadMore(cursor: string) {
    void action.run(async () => {
      const page = await client.admin.deadLetters(serverKey, { cursor });
      setMore((m) => ({ items: [...(m?.items ?? []), ...page.items], next: page.nextCursor }));
    });
  }
  return (
    <section className="mb-6" aria-label={t("diagnostics.deadLetters")}>
      <h2 className="mb-2 font-semibold">{t("diagnostics.deadLetters")}</h2>
      {action.error ? <SetupError error={action.error} /> : null}
      {partial && (
        <p role="status" className="mb-2 text-sm text-muted-foreground">
          {t("diagnostics.deadLettersTruncated", {
            shown: items.length,
            total: Math.max(list.data.total, items.length),
          })}
        </p>
      )}
      <ul className="divide-y rounded-md border px-3">
        {items.map((task) => (
          <li key={task.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
            <div>
              <p className="font-medium">{task.name}</p>
              {task.event && (
                <Button
                  variant="ghost"
                  size="sm"
                  aria-expanded={payload?.id === task.id}
                  onClick={() => {
                    if (payload?.id === task.id) {
                      setPayload(null);
                      return;
                    }
                    void action.run(async () =>
                      setPayload({
                        id: task.id,
                        value: JSON.stringify(
                          await client.admin.deadEvent(serverKey, task.id),
                          null,
                          2,
                        ),
                      }),
                    );
                  }}
                >
                  {t("diagnostics.eventPayload")}
                </Button>
              )}
              {payload?.id === task.id && (
                <pre className="max-h-64 max-w-full overflow-auto text-xs">{payload.value}</pre>
              )}
              <p className="text-xs text-muted-foreground">
                {task.scope} · {t("diagnostics.attempts", { count: task.attempts })} · {task.reason}
              </p>
            </div>
            {discard === task.id ? (
              <div className="flex flex-wrap items-center gap-2">
                <span>{t("diagnostics.confirmDiscard")}</span>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={action.pending}
                  onClick={() => decide(task.id, "discard")}
                >
                  {t("diagnostics.discardTask")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={action.pending}
                  onClick={() => setDiscard(null)}
                >
                  {t("diagnostics.cancel")}
                </Button>
              </div>
            ) : (
              <div className="flex gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={action.pending}
                  onClick={() => decide(task.id, "retry")}
                >
                  {t("diagnostics.retryTask")}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={action.pending}
                  onClick={() => setDiscard(task.id)}
                >
                  {t("diagnostics.discardTask")}
                </Button>
              </div>
            )}
          </li>
        ))}
      </ul>
      {next !== null && (
        <Button
          className="mt-2"
          size="sm"
          variant="outline"
          disabled={action.pending}
          onClick={() => loadMore(next)}
        >
          {t("diagnostics.loadMore")}
        </Button>
      )}
    </section>
  );
}

function Interop({ serverKey }: { serverKey: string }) {
  const { client } = useCicord();
  const { t } = useTranslation();
  const graph = usePoll((signal) => client.admin.interop(serverKey, { signal }), 15_000);
  if (graph.error) return <SetupError error={graph.error} />;
  if (!graph.data) return null;
  const disabled = Object.entries(graph.data.features).flatMap(([id, features]) =>
    Object.entries(features ?? {})
      .filter(([, reason]) => reason)
      .map(([name, reason]) => `${id} · ${name} · ${reason}`),
  );
  if (!graph.data.edges.length && !disabled.length) return null;
  return (
    <section className="mb-6" aria-label={t("diagnostics.interop")}>
      <h2 className="mb-2 font-semibold">{t("diagnostics.interop")}</h2>
      <ul className="divide-y rounded-md border px-3">
        {graph.data.edges.map((edge) => (
          <li key={JSON.stringify(edge)} className="py-2">
            <p className="font-medium">
              {edge.from} → {edge.to}
            </p>
            <p className="text-sm">
              {edge.kind} · {edge.contract} · {edge.status}
            </p>
          </li>
        ))}
      </ul>
      {disabled.length > 0 && (
        <>
          <h3 className="mt-3 font-medium">{t("diagnostics.disabledFeatures")}</h3>
          <ul>
            {disabled.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
