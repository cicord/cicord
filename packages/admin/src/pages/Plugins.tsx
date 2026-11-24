/** Plugin instances of a server: a server-side table; each row opens the instance's page. */
import type { PluginInstance, PluginListParams, PluginSortKey } from "@cicord/client";
import { useCicord } from "@cicord/react";
import { Puzzle } from "lucide-react";
import { keys } from "../api.ts";
import { EmptyState, Mono, Page, PageHeader } from "../components/common.tsx";
import { DataTable, type DataTableColumn, type TableQuery } from "../components/data-table.tsx";
import { HealthBadge } from "../components/status.tsx";
import { TrustBadge } from "../components/trust.tsx";
import { UpdateBadge } from "../components/update.tsx";
import { useTranslation } from "../i18n/index.ts";
import { useFlash } from "../lib/flash.ts";
import { instancePath } from "../shell/nav.ts";
import { UninstallNotice, type UninstallFlash } from "./PluginInstance.tsx";

export { formatPath, shortRev } from "./PluginSettings.tsx";

export const pluginParams = (q: TableQuery): PluginListParams => ({
  ...(q.q ? { q: q.q } : {}),
  sort: q.sort as PluginSortKey,
  order: q.order,
  offset: q.offset,
  limit: q.limit,
  ...(q.filters["updates"] ? { updates: q.filters["updates"] === "true" } : {}),
});

export function PluginsPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const { client } = useCicord();
  // The result of an uninstall made on the instance's page, shown once.
  const [flash, dismissFlash] = useFlash<UninstallFlash>(`uninstall:${serverKey}`);

  const columns: DataTableColumn<PluginInstance>[] = [
    {
      id: "plugin",
      header: t("plugins.columns.plugin"),
      sortKey: "name",
      primary: true,
      className: "min-w-64",
      cell: (p) => (
        <span className="flex min-w-0 flex-col gap-0.5">
          <span className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-medium text-foreground">
              {p.plugin.displayName || p.plugin.name}
            </span>
            <Mono className="text-[11px]">
              {p.plugin.name}@{p.plugin.version}
            </Mono>
          </span>
          {p.plugin.description ? (
            <span className="line-clamp-1 max-w-[46ch] text-xs text-muted-foreground">
              {p.plugin.description}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      id: "instance",
      header: t("plugins.columns.instance"),
      sortKey: "instance",
      skeleton: "w-20",
      cell: (p) => <Mono className="text-foreground">{p.instance}</Mono>,
    },
    {
      id: "bot",
      header: t("plugins.columns.bot"),
      sortKey: "bot",
      skeleton: "w-20",
      cell: (p) => <Mono>{p.bot}</Mono>,
    },
    {
      id: "version",
      header: t("plugins.columns.version"),
      sortKey: "version",
      skeleton: "w-12",
      cell: (p) => (
        <span className="inline-flex flex-wrap items-center gap-1.5">
          <Mono>{p.plugin.version}</Mono>
          {p.update ? <UpdateBadge update={p.update} /> : null}
        </span>
      ),
    },
    {
      id: "source",
      header: t("plugins.columns.source"),
      sortKey: "source",
      skeleton: "w-24",
      cell: (p) => (
        <span className="flex flex-wrap items-center gap-1.5">
          {p.source && p.source !== "local" ? <Mono>{p.source}</Mono> : null}
          <TrustBadge trust={p.trust} />
        </span>
      ),
    },
    {
      id: "health",
      header: t("plugins.columns.health"),
      sortKey: "health",
      skeleton: "w-20",
      cell: (p) => <HealthBadge health={p.health} />,
    },
    {
      id: "commands",
      header: t("plugins.columns.commands"),
      className: "text-right",
      skeleton: "ml-auto w-6",
      cell: (p) =>
        p.commands.length ? (
          <span className="tabular">{p.commands.length}</span>
        ) : (
          <span className="text-subtle-foreground">—</span>
        ),
    },
  ];

  return (
    <Page>
      <PageHeader title={t("plugins.title")} description={t("plugins.description")} />
      {flash ? <UninstallNotice flash={flash} onDismiss={dismissFlash} /> : null}
      <DataTable
        label={t("plugins.table")}
        columns={columns}
        queryKey={(q) => keys.pluginsPage(serverKey, pluginParams(q))}
        fetch={(q, signal) => client.admin.pluginsPage(serverKey, pluginParams(q), { signal })}
        rowKey={(p) => `${p.bot}/${p.instance}`}
        rowHref={(p) => instancePath(serverKey, p.bot, p.instance)}
        defaultSort="name"
        rememberAs={`plugins:${serverKey}`}
        search={{ label: t("plugins.search.label"), placeholder: t("plugins.search.placeholder") }}
        filters={[
          {
            id: "updates",
            label: t("plugins.filters.updates"),
            allLabel: t("plugins.filters.anyUpdates"),
            options: [
              { value: "true", label: t("plugins.filters.updatesYes") },
              { value: "false", label: t("plugins.filters.updatesNo") },
            ],
          },
        ]}
        empty={
          <EmptyState icon={<Puzzle />} title={t("plugins.empty.title")}>
            {t("plugins.empty.text")}
          </EmptyState>
        }
      />
    </Page>
  );
}
