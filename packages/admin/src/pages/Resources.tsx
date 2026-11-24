/** Roles & channels: what exists on Discord, and which part Cicord manages. */
import type { ServerResources } from "@cicord/client";
import { FolderClosed } from "lucide-react";
import { useState } from "react";
import { useResources } from "../api.ts";
import { EmptyState, LoadingRows, Mono, Page, PageHeader } from "../components/common.tsx";
import { ErrorState } from "../components/errors.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Card, CardHeader, CardTitle } from "../components/ui/card.tsx";
import { ToggleGroup, ToggleGroupItem } from "../components/ui/toggle-group.tsx";
import { Tip } from "../components/ui/tooltip.tsx";
import { useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import { ChannelGlyph, Swatch } from "../schema-form/index.ts";

type ResFilter = "all" | "managed" | "unmanaged";

const keep = (managed: boolean, filter: ResFilter) =>
  filter === "all" || (filter === "managed" ? managed : !managed);

export function ResourcesPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const res = useResources(serverKey);
  const [filter, setFilter] = useState<ResFilter>("all");
  const counts = res.data
    ? {
        roles: res.data.roles.length,
        channels: res.data.channels.length + res.data.categories.length,
        unmanaged:
          res.data.roles.filter((r) => !r.managed).length +
          res.data.channels.filter((c) => !c.managed).length +
          res.data.categories.filter((c) => !c.managed).length,
      }
    : null;
  return (
    <Page>
      <PageHeader
        title={t("resources.title")}
        description={t("resources.description", { file: `servers/${serverKey}.yaml` })}
        meta={
          counts ? (
            <span className="text-xs text-muted-foreground">
              {t("resources.counts", {
                roles: counts.roles,
                channels: counts.channels,
                unmanaged: counts.unmanaged,
              })}
            </span>
          ) : null
        }
        actions={
          <ToggleGroup
            type="single"
            value={filter}
            onValueChange={(v) => v && setFilter(v as ResFilter)}
            aria-label={t("resources.filter.label")}
          >
            {(["all", "managed", "unmanaged"] as const).map((f) => (
              <ToggleGroupItem key={f} value={f}>
                {t(`resources.filter.${f}`)}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        }
      />
      {res.isLoading ? (
        <Card>
          <LoadingRows rows={8} />
        </Card>
      ) : !res.data ? (
        <ErrorState error={res.error} onRetry={() => void res.refetch()} />
      ) : (
        <div className="grid items-start gap-6 lg:grid-cols-2">
          <Card aria-labelledby="roles-title">
            <CardHeader className="py-2.5">
              <CardTitle id="roles-title">{t("resources.roles")}</CardTitle>
              <span className="ml-auto text-xs leading-6 text-muted-foreground">
                {t("resources.rolesHint")}
              </span>
            </CardHeader>
            <RoleList resources={res.data} filter={filter} />
          </Card>
          <Card aria-labelledby="channels-title">
            <CardHeader className="py-2.5">
              <CardTitle id="channels-title">{t("resources.channels")}</CardTitle>
            </CardHeader>
            <ChannelTree resources={res.data} filter={filter} />
          </Card>
        </div>
      )}
    </Page>
  );
}

function ManagedTag({ managed, k }: { managed: boolean; k: string }) {
  const { t } = useTranslation();
  if (managed)
    return (
      <Tip content={t("resources.managedHint")}>
        <Mono className="ml-auto shrink-0">{k}</Mono>
      </Tip>
    );
  return (
    <Tip content={t("resources.unmanagedHint")}>
      <Badge tone="neutral" className="ml-auto border-dashed" tabIndex={0}>
        {t("resources.unmanaged")}
      </Badge>
    </Tip>
  );
}

const row = "flex min-h-9 items-center gap-2.5 px-4 py-1.5 text-[13px]";

function RoleList({ resources, filter }: { resources: ServerResources; filter: ResFilter }) {
  const { t } = useTranslation();
  const roles = resources.roles.filter((r) => keep(r.managed, filter));
  if (roles.length === 0) return <EmptyState title={t("resources.noRoles")} />;
  return (
    <ul className="divide-y">
      {roles.map((r) => (
        <li key={r.id || r.key} className={cn(row, !r.managed && "text-muted-foreground")}>
          <Swatch color={r.color} />
          <span
            className="truncate font-medium"
            style={{ color: r.managed && r.color && r.color !== "#000000" ? r.color : undefined }}
          >
            {r.name}
          </span>
          <ManagedTag managed={r.managed} k={r.key} />
        </li>
      ))}
    </ul>
  );
}

function ChannelTree({ resources, filter }: { resources: ServerResources; filter: ResFilter }) {
  const { t } = useTranslation();
  const channels = resources.channels.filter(
    (c) => keep(c.managed, filter) && c.type !== "category",
  );
  const known = new Set(resources.categories.map((c) => c.key).filter(Boolean));
  const loose = channels.filter((c) => !c.category || !known.has(c.category));
  const cats = resources.categories.filter(
    (cat) => keep(cat.managed, filter) || channels.some((c) => c.category === cat.key && cat.key),
  );
  if (cats.length === 0 && loose.length === 0)
    return <EmptyState title={t("resources.noChannels")} />;
  return (
    <ul className="divide-y">
      {loose.map((c) => (
        <ChannelItem key={c.id || c.key} c={c} />
      ))}
      {cats.map((cat) => {
        const children = channels.filter((c) => cat.key && c.category === cat.key);
        return (
          <li key={cat.id || cat.key}>
            <div className={cn(row, "bg-surface-2/60", !cat.managed && "text-muted-foreground")}>
              <FolderClosed className="size-4 text-muted-foreground" aria-hidden="true" />
              <span className="truncate text-xs font-semibold">{cat.name}</span>
              <ManagedTag managed={cat.managed} k={cat.key} />
            </div>
            {children.length ? (
              <ul className="divide-y">
                {children.map((c) => (
                  <ChannelItem key={c.id || c.key} c={c} nested />
                ))}
              </ul>
            ) : (
              <p className="px-4 py-2 pl-10 text-xs text-subtle-foreground">
                {t("resources.emptyCategory")}
              </p>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function ChannelItem({ c, nested }: { c: ServerResources["channels"][number]; nested?: boolean }) {
  return (
    <li className={cn(row, nested && "pl-8", !c.managed && "text-muted-foreground")}>
      <ChannelGlyph type={c.type} />
      <span className="truncate">{c.name}</span>
      {c.type && c.type !== "text" ? (
        <span className="text-xs text-subtle-foreground">{c.type}</span>
      ) : null}
      <ManagedTag managed={c.managed} k={c.key} />
    </li>
  );
}
