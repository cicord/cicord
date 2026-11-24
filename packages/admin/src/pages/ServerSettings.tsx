/**
 * Server settings: the live Discord state (boost level, Community), the server images
 * (uploaded through write-back), and everything the server YAML declares (read only), plus
 * the settings only the Discord app can change.
 */
import type { Json } from "@cicord/client";
import { FileCode2, ShieldCheck } from "lucide-react";
import type { ReactNode } from "react";
import { useServer } from "../api.ts";
import { LoadingRows, Mono, Page, PageHeader } from "../components/common.tsx";
import {
  AssetRow,
  BoostPanel,
  CommunityPanel,
  DiscordOnlyCard,
  slotsAt,
} from "../components/discord.tsx";
import { Callout, ErrorState } from "../components/errors.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Card, CardDescription, CardHeader, CardTitle } from "../components/ui/card.tsx";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { ValueView } from "./Sync.tsx";

type Obj = { [key: string]: Json };
const obj = (v: Json | undefined): Obj =>
  v && typeof v === "object" && !Array.isArray(v) ? (v as Obj) : {};
const list = (v: Json | undefined): string[] => (Array.isArray(v) ? v.map(String) : []);

export function ServerSettingsPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
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
  const community = obj(spec["community"]);
  const afk = obj(spec["afk"]);
  const widget = obj(spec["widget"]);
  const flags = obj(profile["systemChannelFlags"]);
  const automod = obj(spec["automod"]);
  const onboarding = obj(spec["onboarding"]);
  const welcome = obj(spec["welcomeScreen"]);
  const slots = slotsAt(server.data.boostLevel);
  const str = (v: Json | undefined) => (typeof v === "string" ? v : "");

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

      <div className="grid items-start gap-6 lg:grid-cols-2">
        <BoostPanel server={server.data} />
        <CommunityPanel
          server={server.data}
          declared={"community" in spec ? community["enabled"] === true : undefined}
        />
      </div>

      <Card aria-labelledby="assets-title">
        <CardHeader className="flex-col gap-0.5">
          <CardTitle id="assets-title">{t("parity.assets.title")}</CardTitle>
          <CardDescription>{t("parity.assets.description")}</CardDescription>
        </CardHeader>
        <div className="divide-y border-t">
          {(["icon", "banner", "splash"] as const).map((kind) => (
            <AssetRow
              key={kind}
              serverKey={serverKey}
              kind={kind}
              server={server.data!}
              declared={str(profile[kind])}
              revision={ws.status?.revision?.id}
              canEdit={ws.isAdmin}
            />
          ))}
        </div>
      </Card>

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
        <Row label={t("parity.yaml.banner")} value={profile["banner"]} mono />
        <Row label={t("parity.yaml.splash")} value={profile["splash"]} mono />
        <Row
          label={t("parity.yaml.explicitContentFilter")}
          value={profile["explicitContentFilter"]}
        />
        <Row label={t("parity.yaml.preferredLocale")} value={profile["preferredLocale"]} />
        <Row label={t("parity.yaml.premiumProgressBar")} value={profile["premiumProgressBar"]} />
        <Row
          label={t("parity.yaml.systemChannelFlags")}
          value={undefined}
          custom={
            Object.keys(flags).length ? (
              <span className="flex flex-wrap gap-1.5">
                {Object.entries(flags).map(([k, v]) => (
                  <Badge key={k} tone={v === true ? "success" : "neutral"}>
                    {t(`parity.yaml.flags.${k}`, { defaultValue: k })}:{" "}
                    {v === true ? t("common.on") : t("common.off")}
                  </Badge>
                ))}
              </span>
            ) : undefined
          }
        />
      </Section>

      <Section
        title={t("parity.yaml.engagement.title")}
        description={t("parity.yaml.engagement.description")}
      >
        <Row label={t("parity.yaml.engagement.afkChannel")} value={afk["channel"]} mono />
        <Row
          label={t("parity.yaml.engagement.afkTimeout")}
          value={
            typeof afk["timeout"] === "number"
              ? t("parity.yaml.engagement.seconds", { count: afk["timeout"] })
              : undefined
          }
        />
        <Row
          label={t("parity.yaml.engagement.widget")}
          value={"widget" in spec ? widget["enabled"] === true : undefined}
        />
        <Row label={t("parity.yaml.engagement.widgetChannel")} value={widget["channel"]} mono />
      </Section>

      <Section
        title={t("parity.yaml.community.title")}
        description={t("parity.yaml.community.description")}
      >
        <Row
          label={t("parity.yaml.community.enabled")}
          value={"community" in spec ? community["enabled"] === true : undefined}
        />
        <Row
          label={t("parity.yaml.community.rulesChannel")}
          value={community["rulesChannel"]}
          mono
        />
        <Row
          label={t("parity.yaml.community.updatesChannel")}
          value={community["updatesChannel"]}
          mono
        />
        <Row
          label={t("parity.yaml.community.safetyAlertsChannel")}
          value={community["safetyAlertsChannel"]}
          mono
        />
      </Section>

      <Section
        title={t("parity.yaml.automod.title")}
        description={t("parity.yaml.automod.description")}
      >
        {Object.keys(automod).length === 0 ? (
          <p className="px-4 py-2.5 text-[13px] text-muted-foreground">
            {t("parity.yaml.automod.empty")}
          </p>
        ) : (
          Object.entries(automod).map(([key, v]) => {
            const rule = obj(v);
            const actions = obj(rule["actions"]);
            const parts: string[] = [];
            if ("block" in actions) parts.push(t("parity.yaml.automod.block"));
            if (typeof actions["alert"] === "string")
              parts.push(t("parity.yaml.automod.alert", { channel: actions["alert"] }));
            if (typeof actions["timeout"] === "string")
              parts.push(t("parity.yaml.automod.timeout", { duration: actions["timeout"] }));
            return (
              <Row
                key={key}
                label={str(rule["name"]) || key}
                value={undefined}
                custom={
                  <span className="flex flex-wrap items-center gap-1.5">
                    <Badge tone="info">
                      {t(`parity.yaml.automod.triggers.${str(rule["trigger"])}`, {
                        defaultValue: str(rule["trigger"]),
                      })}
                    </Badge>
                    {rule["enabled"] === false ? (
                      <Badge tone="neutral">{t("parity.yaml.automod.disabled")}</Badge>
                    ) : null}
                    <span className="text-muted-foreground">
                      {t("parity.yaml.automod.actions")}: {parts.join(", ") || "—"}
                    </span>
                    <Mono className="text-[11px]">{key}</Mono>
                  </span>
                }
              />
            );
          })
        )}
      </Section>

      <Section
        title={t("parity.yaml.onboarding.title")}
        description={t("parity.yaml.onboarding.description")}
      >
        <Row
          label={t("parity.yaml.onboarding.onboarding")}
          value={"onboarding" in spec ? onboarding["enabled"] === true : undefined}
        />
        <Row label={t("parity.yaml.onboarding.mode")} value={onboarding["mode"]} />
        <Row
          label={t("parity.yaml.onboarding.defaultChannels")}
          value={undefined}
          custom={<KeyList keys={list(onboarding["defaultChannels"])} />}
        />
        <Row
          label={t("parity.yaml.onboarding.prompts")}
          value={undefined}
          custom={
            <KeyList
              keys={(Array.isArray(onboarding["prompts"]) ? onboarding["prompts"] : []).map(
                (p) => str(obj(p)["title"]) || str(obj(p)["key"]),
              )}
              plain
            />
          }
        />
        <Row
          label={t("parity.yaml.onboarding.welcomeScreen")}
          value={"welcomeScreen" in spec ? welcome["enabled"] !== false : undefined}
        />
        <Row
          label={t("parity.yaml.onboarding.welcomeChannels")}
          value={undefined}
          custom={
            <KeyList
              keys={(Array.isArray(welcome["channels"]) ? welcome["channels"] : []).map((c) =>
                str(obj(c)["channel"]),
              )}
            />
          }
        />
      </Section>

      <Section
        title={t("parity.yaml.expressions.title")}
        description={t("parity.yaml.expressions.description")}
      >
        <Row
          label={t("parity.yaml.expressions.emojis")}
          value={t("parity.yaml.expressions.ofLimit", {
            count: count("emojis"),
            limit: slots.emojis,
          })}
        />
        <Row
          label={t("parity.yaml.expressions.stickers")}
          value={t("parity.yaml.expressions.ofLimit", {
            count: count("stickers"),
            limit: slots.stickers,
          })}
        />
        <Row
          label={t("parity.yaml.expressions.sounds")}
          value={t("parity.yaml.expressions.ofLimit", {
            count: count("soundboard"),
            limit: slots.sounds,
          })}
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

      <DiscordOnlyCard />
    </Page>
  );
}

function KeyList({ keys, plain }: { keys: string[]; plain?: boolean }) {
  if (keys.length === 0) return <span className="text-subtle-foreground">—</span>;
  return (
    <span className="flex flex-wrap gap-1">
      {keys.map((k, i) => (
        <Badge key={`${k}-${i}`} tone="neutral" className={plain ? undefined : "font-mono"}>
          {k}
        </Badge>
      ))}
    </span>
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
