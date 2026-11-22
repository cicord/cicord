/** Server overview: state and problems first, then the facts, recent activity and bots. */
import type { BotStatus, ServerDetail } from "@codi/client";
import { ArrowRight, CircleHelp, History } from "lucide-react";
import { useMemo } from "react";
import { useHistory, useServer, useStatus } from "../api.ts";
import {
  BotAvatar,
  EmptyState,
  Fact,
  LoadingRows,
  Mono,
  Page,
  PageHeader,
  PluginGlyph,
  ServerAvatar,
  Sha,
  Time,
} from "../components/common.tsx";
import { LiveSummary } from "../components/discord.tsx";
import { ErrorState, HumanErrorCallout } from "../components/errors.tsx";
import { HistoryList } from "../components/history.tsx";
import {
  HealthBadge,
  StatusDot,
  StatusLegend,
  SyncBadge,
  healthTone,
} from "../components/status.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card, CardAction, CardHeader, CardTitle } from "../components/ui/card.tsx";
import { Skeleton } from "../components/ui/skeleton.tsx";
import { Tip } from "../components/ui/tooltip.tsx";
import { RoleBadge, useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { errorOf } from "../lib/errors.ts";
import { groupHistory } from "../lib/history.ts";
import { Link } from "../router.tsx";
import { instancePath } from "../shell/nav.ts";
import { relevantHistory, SyncButton } from "./Sync.tsx";

export function OverviewPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const server = useServer(serverKey);
  const status = useStatus();
  const history = useHistory(100);

  const bots = useMemo(
    () => (status.data?.bots ?? []).filter((b) => b.servers.includes(serverKey)),
    [status.data, serverKey],
  );
  const records = useMemo(
    () =>
      relevantHistory(
        history.data ?? [],
        serverKey,
        bots.map((b) => b.key),
      ),
    [history.data, serverKey, bots],
  );

  if (server.isLoading)
    return (
      <Page>
        <div className="flex items-center gap-3">
          <Skeleton className="size-10 rounded-[30%]" />
          <div className="flex flex-col gap-2">
            <Skeleton className="h-5 w-48" />
            <Skeleton className="h-4 w-72" />
          </div>
        </div>
        <Card>
          <LoadingRows rows={5} />
        </Card>
      </Page>
    );
  if (!server.data)
    return (
      <Page>
        <PageHeader title={serverKey} />
        <ErrorState error={server.error} onRetry={() => void server.refetch()} />
      </Page>
    );

  const s = server.data;
  const error = s.health === "Degraded" || s.health === "Missing" ? errorOf(s) : null;
  // How often the current failure repeated (newest group of this server's syncs).
  const serverGroups = groupHistory(records.filter((r) => r.target === `server:${serverKey}`));
  const run = serverGroups[0] && serverGroups[0].status === "failed" ? serverGroups[0] : undefined;
  const rev = status.data?.revision;
  const lastError =
    status.data?.lastErrorInfo ??
    (status.data?.lastError ? errorOf({ message: status.data.lastError }) : null);

  return (
    <Page>
      <PageHeader
        leading={<ServerAvatar name={s.name} url={s.iconUrl} size={40} className="mt-0.5" />}
        title={s.name}
        meta={
          <>
            <SyncBadge sync={s.sync} />
            <HealthBadge health={s.health} />
            <RoleBadge role={ws.role} />
            <span className="text-xs text-muted-foreground">
              <Mono>{s.key}</Mono>
              <span className="mx-1.5 text-subtle-foreground">/</span>
              {t("overview.discordId")} <Mono className="tabular">{s.discordId}</Mono>
            </span>
          </>
        }
        actions={
          <>
            <StatusLegend>
              <Button variant="ghost" size="sm" className="text-muted-foreground">
                <CircleHelp />
                <span className="hidden sm:inline">{t("legend.open")}</span>
              </Button>
            </StatusLegend>
            {ws.isAdmin ? <SyncButton serverKey={s.key} /> : null}
          </>
        }
      />

      {lastError ? (
        <HumanErrorCallout
          tone="warning"
          title={t("overview.rejected.title")}
          error={{
            ...lastError,
            hint: lastError.hint ?? t("overview.rejected.hint"),
          }}
        />
      ) : null}

      {error ? (
        <HumanErrorCallout
          error={error}
          meta={
            run && run.count > 1 ? (
              <span>
                {t("overview.failedTimes", { count: run.count })} · {t("history.last")}{" "}
                <Time iso={run.latest.time} />
              </span>
            ) : s.lastSync ? (
              <Time iso={s.lastSync} />
            ) : undefined
          }
        />
      ) : null}

      <Card>
        <dl className="grid grid-cols-2 divide-border lg:grid-cols-4 lg:divide-x [&>*:nth-child(-n+2)]:border-b lg:[&>*:nth-child(-n+2)]:border-b-0 [&>*:nth-child(odd)]:border-r lg:[&>*:nth-child(odd)]:border-r-0">
          <Fact label={t("overview.facts.revision")}>
            {rev ? (
              <span className="flex min-w-0 items-center gap-2">
                <Sha id={rev.id} className="rev-id text-foreground" />
                <span className="truncate font-normal text-muted-foreground" title={rev.message}>
                  {rev.message.split("\n")[0] || t("overview.noMessage")}
                </span>
              </span>
            ) : (
              <span className="font-normal text-muted-foreground">{t("overview.noRevision")}</span>
            )}
          </Fact>
          <Fact label={t("overview.facts.lastSync")}>
            <Time iso={s.lastSync} />
            {s.bot ? (
              <span className="font-normal text-muted-foreground">
                {" "}
                · {t("overview.by", { bot: s.bot })}
              </span>
            ) : null}
          </Fact>
          <Fact label={t("overview.facts.drift")}>
            {s.drift.length === 0 ? (
              <span className="font-normal text-muted-foreground">{t("overview.noDrift")}</span>
            ) : (
              <Link to={`/servers/${s.key}/drift`} className="text-warning hover:underline">
                {t("overview.driftCount", { count: s.drift.length })}
              </Link>
            )}
          </Fact>
          <PluginsFact server={s} bots={bots} />
        </dl>
      </Card>

      <LiveSummary
        server={s}
        action={
          <CardAction>
            <Button variant="ghost" size="sm" asChild>
              <Link to={`/servers/${s.key}/settings`}>
                {t("parity.live.details")}
                <ArrowRight />
              </Link>
            </Button>
          </CardAction>
        }
      />

      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <Card aria-labelledby="activity-title">
          <CardHeader className="items-center py-2.5">
            <CardTitle id="activity-title">{t("overview.activity")}</CardTitle>
            <CardAction>
              <Button variant="ghost" size="sm" asChild>
                <Link to={`/servers/${s.key}/history`}>
                  {t("overview.viewHistory")}
                  <ArrowRight />
                </Link>
              </Button>
            </CardAction>
          </CardHeader>
          {history.isLoading ? (
            <LoadingRows rows={5} />
          ) : !history.data ? (
            <div className="p-4">
              <ErrorState error={history.error} onRetry={() => void history.refetch()} />
            </div>
          ) : records.length === 0 ? (
            <EmptyState icon={<History />} title={t("history.empty.title")} />
          ) : (
            <HistoryList records={records} limit={8} />
          )}
        </Card>

        <Card aria-labelledby="bots-title">
          <CardHeader className="items-center py-2.5">
            <CardTitle id="bots-title">{t("overview.bots")}</CardTitle>
          </CardHeader>
          {bots.length === 0 ? (
            <EmptyState title={t("overview.noBots.title")}>{t("overview.noBots.text")}</EmptyState>
          ) : (
            <ul className="divide-y">
              {bots.map((b) => (
                <BotItem key={b.key} bot={b} serverKey={s.key} />
              ))}
            </ul>
          )}
        </Card>
      </div>
    </Page>
  );
}

function PluginsFact({ server, bots }: { server: ServerDetail; bots: BotStatus[] }) {
  const { t } = useTranslation();
  const instances = bots.flatMap((b) => b.plugins.filter((p) => p.server === server.key));
  const unhealthy = instances.filter((p) => p.health !== "Healthy").length;
  return (
    <Fact label={t("overview.facts.plugins")}>
      <Link to={`/servers/${server.key}/plugins`} className="hover:underline">
        {t("overview.pluginCount", { count: instances.length })}
      </Link>
      {unhealthy ? (
        <span className="font-normal text-danger">
          {" "}
          · {t("overview.unhealthy", { count: unhealthy })}
        </span>
      ) : null}
    </Fact>
  );
}

function BotItem({ bot, serverKey }: { bot: BotStatus; serverKey: string }) {
  const { t } = useTranslation();
  const plugins = bot.plugins.filter((p) => p.server === serverKey);
  const error = bot.health === "Degraded" || bot.health === "Missing" ? errorOf(bot) : null;
  return (
    <li className="flex flex-col gap-3 px-4 py-3">
      <div className="flex items-center gap-2.5">
        <BotAvatar size={28} />
        <div className="min-w-0 flex-1">
          <p className="flex items-center gap-1.5 text-[13px] font-medium">
            <span className="truncate">{bot.username || bot.key}</span>
            <Badge tone="brand" className="h-4 px-1 text-[10px]">
              {t("overview.botTag")}
            </Badge>
          </p>
          <Mono className="text-[11px]">{bot.key}</Mono>
        </div>
        <HealthBadge health={bot.health} />
      </div>
      {error ? (
        <div className="rounded-md border border-danger/25 bg-danger-soft px-3 py-2 text-[13px]">
          <p className="font-medium">{error.message}</p>
          {error.hint ? <p className="mt-0.5 text-muted-foreground">{error.hint}</p> : null}
        </div>
      ) : null}
      {plugins.length ? (
        <ul
          className="flex flex-col gap-1"
          aria-label={t("overview.instancesOf", { bot: bot.username || bot.key })}
        >
          {plugins.map((p) => (
            <li key={p.instance}>
              <Link
                to={instancePath(serverKey, bot.key, p.instance)}
                className="group flex items-center gap-2.5 rounded-md px-1.5 py-1 -mx-1.5 hover:bg-surface-hover"
              >
                <PluginGlyph size={22} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px]">{p.instance}</span>
                  <span className="block truncate font-mono text-[11px] text-subtle-foreground">
                    {p.plugin}@{p.version}
                  </span>
                </span>
                <Tip
                  content={
                    p.message || t(`status.health.${p.health}.label`, { defaultValue: p.health })
                  }
                >
                  <span tabIndex={0} className="p-1">
                    <StatusDot
                      tone={healthTone(p.health)}
                      label={t(`status.health.${p.health}.label`, { defaultValue: p.health })}
                    />
                  </span>
                </Tip>
              </Link>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-[13px] text-muted-foreground">{t("overview.noInstances")}</p>
      )}
    </li>
  );
}
