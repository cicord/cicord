/** Server settings: the profile, sync policy and access declared in the server YAML (read only). */
import type { Json } from "@codi/client";
import { FileCode2, ShieldCheck } from "lucide-react";
import type { ReactNode } from "react";
import { useServer } from "../api.ts";
import { LoadingRows, Mono, Page, PageHeader } from "../components/common.tsx";
import { Callout, ErrorState } from "../components/errors.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Card, CardDescription, CardHeader, CardTitle } from "../components/ui/card.tsx";
import { useTranslation } from "../i18n/index.ts";
import { ValueView } from "./Sync.tsx";

type Obj = { [key: string]: Json };
const obj = (v: Json | undefined): Obj =>
  v && typeof v === "object" && !Array.isArray(v) ? (v as Obj) : {};
const list = (v: Json | undefined): string[] => (Array.isArray(v) ? v.map(String) : []);

export function ServerSettingsPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const server = useServer(serverKey);
  const file = `servers/${serverKey}.yaml`;
  if (server.isLoading)
    return (
      <Page>
        <PageHeader title={t("serverSettings.title")} />
        <Card>
          <LoadingRows rows={6} />
        </Card>
      </Page>
    );
  if (!server.data)
    return (
      <Page>
        <PageHeader title={t("serverSettings.title")} />
        <ErrorState error={server.error} onRetry={() => void server.refetch()} />
      </Page>
    );
  const spec = server.data.spec;
  const profile = obj(spec["profile"]);
  const policy = obj(spec["syncPolicy"]);
  const access = obj(spec["access"]);
  const everyone = obj(spec["everyone"]);
  const count = (k: string) => Object.keys(obj(spec[k])).length;

  return (
    <Page>
      <PageHeader
        title={t("serverSettings.title")}
        description={t("serverSettings.description")}
        meta={
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <FileCode2 className="size-3.5" aria-hidden="true" />
            <Mono>{file}</Mono>
          </span>
        }
      />
      <Callout tone="info" title={t("serverSettings.readOnly.title")}>
        {t("serverSettings.readOnly.text", { file })}
      </Callout>

      <Section
        title={t("serverSettings.profile.title")}
        description={t("serverSettings.profile.description")}
      >
        <Row label={t("serverSettings.profile.name")} value={profile["name"]} />
        <Row label={t("serverSettings.profile.description2")} value={profile["description"]} />
        <Row label={t("serverSettings.profile.icon")} value={profile["icon"]} mono />
        <Row
          label={t("serverSettings.profile.verificationLevel")}
          value={profile["verificationLevel"]}
        />
        <Row
          label={t("serverSettings.profile.defaultNotifications")}
          value={profile["defaultNotifications"]}
        />
        <Row
          label={t("serverSettings.profile.systemChannel")}
          value={profile["systemChannel"]}
          mono
        />
        <Row
          label={t("serverSettings.profile.rulesChannel")}
          value={profile["rulesChannel"]}
          mono
        />
      </Section>

      <Section
        title={t("serverSettings.policy.title")}
        description={t("serverSettings.policy.description")}
      >
        <PolicyRow
          label={t("serverSettings.policy.selfHeal")}
          on={policy["selfHeal"] === true}
          help={t("serverSettings.policy.selfHealHelp")}
        />
        <PolicyRow
          label={t("serverSettings.policy.prune")}
          on={policy["prune"] === true}
          help={t("serverSettings.policy.pruneHelp")}
        />
        <PolicyRow
          label={t("serverSettings.policy.deletionProtection")}
          on={policy["deletionProtection"] !== false}
          help={t("serverSettings.policy.deletionProtectionHelp")}
        />
      </Section>

      <Section
        title={t("serverSettings.access.title")}
        description={t("serverSettings.access.description")}
      >
        {(["admins", "moderators"] as const).map((k) => {
          const a = obj(access[k]);
          const roles = list(a["roles"]);
          const users = list(a["users"]);
          return (
            <Row
              key={k}
              label={t(`serverSettings.access.${k}`)}
              value={undefined}
              custom={
                roles.length || users.length ? (
                  <span className="flex flex-wrap gap-1.5">
                    {roles.map((r) => (
                      <Badge key={`r-${r}`} tone="neutral">
                        <ShieldCheck aria-hidden="true" />
                        {r}
                      </Badge>
                    ))}
                    {users.map((u) => (
                      <Badge key={`u-${u}`} tone="neutral" className="font-mono tabular">
                        {u}
                      </Badge>
                    ))}
                  </span>
                ) : (
                  <span className="text-subtle-foreground">
                    {k === "admins" ? t("serverSettings.access.ownerOnly") : "—"}
                  </span>
                )
              }
            />
          );
        })}
      </Section>

      <Section
        title={t("serverSettings.declared.title")}
        description={t("serverSettings.declared.description")}
      >
        <Row label={t("serverSettings.declared.roles")} value={count("roles")} />
        <Row label={t("serverSettings.declared.categories")} value={count("categories")} />
        <Row label={t("serverSettings.declared.channels")} value={count("channels")} />
        <Row
          label={t("serverSettings.declared.everyone")}
          value={undefined}
          custom={
            list(everyone["permissions"]).length ? (
              <span className="flex flex-wrap gap-1">
                {list(everyone["permissions"]).map((p) => (
                  <Badge key={p} tone="solid" className="font-mono text-[11px] font-normal">
                    {p}
                  </Badge>
                ))}
              </span>
            ) : (
              <span className="text-subtle-foreground">
                {t("serverSettings.declared.notManaged")}
              </span>
            )
          }
        />
      </Section>
    </Page>
  );
}

function Section({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader className="flex-col gap-0.5">
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <dl className="divide-y">{children}</dl>
    </Card>
  );
}

function Row({
  label,
  value,
  mono,
  custom,
}: {
  label: string;
  value: Json | number | undefined;
  mono?: boolean;
  custom?: ReactNode;
}) {
  return (
    <div className="grid gap-1 px-4 py-2.5 sm:grid-cols-[14rem_1fr] sm:gap-4">
      <dt className="text-[13px] text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-[13px]">
        {custom ??
          (mono && typeof value === "string" ? (
            <Mono className="text-foreground">{value}</Mono>
          ) : (
            <ValueView value={value as Json | undefined} />
          ))}
      </dd>
    </div>
  );
}

function PolicyRow({ label, on, help }: { label: string; on: boolean; help: string }) {
  const { t } = useTranslation();
  return (
    <div className="grid gap-1 px-4 py-2.5 sm:grid-cols-[14rem_1fr] sm:gap-4">
      <dt className="text-[13px] text-muted-foreground">{label}</dt>
      <dd className="flex flex-col gap-0.5 text-[13px]">
        <span>
          <Badge tone={on ? "success" : "neutral"}>{on ? t("common.on") : t("common.off")}</Badge>
        </span>
        <span className="text-muted-foreground">{help}</span>
      </dd>
    </div>
  );
}
