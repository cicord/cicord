/**
 * Live Discord state of a server (boost level, Community) and the server image uploads
 * (docs/discord-parity.md). Shared by the Overview and Server settings pages.
 */
import type { ServerAssetKind, ServerStatus } from "@cicord/client";
import { useCicord } from "@cicord/react";
import { Check, ExternalLink, ImageOff, Lock, Rocket, Upload, Users } from "lucide-react";
import { useId, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { keys } from "../api.ts";
import { useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import { Mono } from "./common.tsx";
import { Callout, toastError } from "./errors.tsx";
import { Badge } from "./ui/badge.tsx";
import { Button } from "./ui/button.tsx";
import { Card, CardDescription, CardHeader, CardTitle } from "./ui/card.tsx";

/** Boosts needed for each level (index = level). */
const BOOST_TIERS = [0, 2, 7, 14] as const;

const BOOST_PERKS = [
  { key: "animatedIcon", level: 1 },
  { key: "splash", level: 1 },
  { key: "banner", level: 2 },
  { key: "roleIcons", level: 2 },
  { key: "roleGradients", level: 2 },
  { key: "animatedBanner", level: 3 },
  { key: "vanity", level: 3 },
] as const;

/** Expression slots per boost level (emojis: static, and as many animated). */
const SLOTS = {
  emojis: [50, 100, 150, 250],
  stickers: [5, 15, 30, 60],
  sounds: [8, 24, 36, 48],
} as const;

/** Boost level needed by each server image. */
const ASSET_LEVEL: Record<ServerAssetKind, number> = { icon: 0, banner: 2, splash: 1 };

const clampLevel = (n: number | undefined) => Math.min(Math.max(n ?? 0, 0), 3);

export function slotsAt(level: number | undefined) {
  const l = clampLevel(level);
  return { emojis: SLOTS.emojis[l]!, stickers: SLOTS.stickers[l]!, sounds: SLOTS.sounds[l]! };
}

/** "Level 1 · 3 boosts" with a progress bar toward the next level. */
function BoostMeter({ server, className }: { server: ServerStatus; className?: string }) {
  const { t } = useTranslation();
  const level = clampLevel(server.boostLevel);
  const count = server.boostCount ?? 0;
  const next = level < 3 ? BOOST_TIERS[level + 1]! : undefined;
  const from = BOOST_TIERS[level]!;
  const pct = next === undefined ? 100 : Math.round(((count - from) / (next - from)) * 100);
  return (
    <div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
      <p className="flex items-center gap-1.5 text-[13px] font-medium">
        <Rocket className="size-3.5 text-violet" aria-hidden="true" />
        {t("parity.boost.summary", {
          level,
          boosts: t("parity.boost.count", { count }),
        })}
      </p>
      <div
        className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2"
        role="progressbar"
        aria-label={t("parity.boost.progress", { level: Math.min(level + 1, 3) })}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.max(0, Math.min(pct, 100))}
      >
        <div
          className="h-full rounded-full bg-violet"
          style={{ width: `${Math.max(0, Math.min(pct, 100))}%` }}
        />
      </div>
      <p className="text-xs text-muted-foreground">
        {next === undefined
          ? t("parity.boost.max")
          : t("parity.boost.toNext", { count: next - count, level: level + 1 })}
      </p>
    </div>
  );
}

/** Community on/off badge. */
function CommunityBadge({ server }: { server: ServerStatus }) {
  const { t } = useTranslation();
  return server.community ? (
    <Badge tone="success">
      <Users aria-hidden="true" />
      {t("parity.community.on")}
    </Badge>
  ) : (
    <Badge tone="neutral">
      <Users aria-hidden="true" />
      {t("parity.community.off")}
    </Badge>
  );
}

/** Compact live state for the Overview: boost meter + Community, one line each. */
export function LiveSummary({ server, action }: { server: ServerStatus; action?: ReactNode }) {
  const { t } = useTranslation();
  return (
    <Card aria-labelledby="live-title">
      <CardHeader className="items-center py-2.5">
        <CardTitle id="live-title">{t("parity.live.title")}</CardTitle>
        {action}
      </CardHeader>
      <div className="grid gap-4 border-t px-4 py-3 sm:grid-cols-2 sm:divide-x sm:[&>*:last-child]:pl-4">
        <div className="flex flex-col gap-1.5">
          <BoostMeter server={server} />
          <p className="text-xs text-muted-foreground">{t("parity.boost.short")}</p>
        </div>
        <div className="flex flex-col gap-1.5">
          <p className="flex items-center gap-2 text-[13px] font-medium">
            {t("parity.community.title")} <CommunityBadge server={server} />
          </p>
          <p className="text-xs text-muted-foreground">{t("parity.community.short")}</p>
        </div>
      </div>
    </Card>
  );
}

/** Boost panel of Server settings: meter, perks unlocked / locked, slots. */
export function BoostPanel({ server }: { server: ServerStatus }) {
  const { t } = useTranslation();
  const level = clampLevel(server.boostLevel);
  const slots = slotsAt(level);
  return (
    <Card>
      <CardHeader className="flex-col gap-0.5">
        <CardTitle>{t("parity.boost.title")}</CardTitle>
        <CardDescription>{t("parity.boost.description")}</CardDescription>
      </CardHeader>
      <div className="flex flex-col gap-3 px-4 pb-4">
        <BoostMeter server={server} />
        <ul className="flex flex-col gap-1" aria-label={t("parity.boost.title")}>
          {BOOST_PERKS.map((p) => {
            const ok = level >= p.level || (server.features ?? []).includes(featureOf(p.key));
            return (
              <li key={p.key} className="flex items-center gap-2 text-[13px]">
                {ok ? (
                  <Check className="size-3.5 text-success" aria-hidden="true" />
                ) : (
                  <Lock className="size-3.5 text-subtle-foreground" aria-hidden="true" />
                )}
                <span className={cn("flex-1", !ok && "text-muted-foreground")}>
                  {t(`parity.boost.perks.${p.key}`)}
                </span>
                <span className="text-xs text-subtle-foreground">
                  {ok ? t("parity.boost.unlocked") : t("parity.boost.locked", { level: p.level })}
                </span>
              </li>
            );
          })}
        </ul>
        <p className="text-xs text-muted-foreground">
          {t("parity.boost.slots")}: {t("parity.boost.slotsValue", slots)}
        </p>
      </div>
    </Card>
  );
}

function featureOf(perk: string): string {
  switch (perk) {
    case "animatedIcon":
      return "ANIMATED_ICON";
    case "splash":
      return "INVITE_SPLASH";
    case "banner":
      return "BANNER";
    case "roleIcons":
      return "ROLE_ICONS";
    case "roleGradients":
      return "ENHANCED_ROLE_COLORS";
    case "animatedBanner":
      return "ANIMATED_BANNER";
    default:
      return "VANITY_URL";
  }
}

/** Community panel of Server settings: live state, what it unlocks, YAML intent. */
export function CommunityPanel({
  server,
  declared,
}: {
  server: ServerStatus;
  /** community.enabled in the YAML (undefined = block absent). */
  declared: boolean | undefined;
}) {
  const { t } = useTranslation();
  return (
    <Card>
      <CardHeader className="flex-col gap-0.5">
        <CardTitle className="flex items-center gap-2">
          {t("parity.community.title")} <CommunityBadge server={server} />
        </CardTitle>
        <CardDescription>{t("parity.community.description")}</CardDescription>
      </CardHeader>
      <div className="flex flex-col gap-3 px-4 pb-4">
        <ul className="flex flex-col gap-1">
          {(["announcement", "channels", "welcome", "onboarding", "discovery"] as const).map(
            (k) => (
              <li key={k} className="flex items-center gap-2 text-[13px]">
                {server.community ? (
                  <Check className="size-3.5 text-success" aria-hidden="true" />
                ) : (
                  <Lock className="size-3.5 text-subtle-foreground" aria-hidden="true" />
                )}
                <span className={cn(!server.community && "text-muted-foreground")}>
                  {t(`parity.community.unlocks.${k}`)}
                </span>
              </li>
            ),
          )}
        </ul>
        <p className="text-xs text-muted-foreground">
          <Badge tone={declared === undefined ? "neutral" : declared ? "success" : "warning"}>
            {declared === undefined
              ? t("parity.community.yamlUnset")
              : declared
                ? t("parity.community.yamlOn")
                : t("parity.community.yamlOff")}
          </Badge>{" "}
          {server.community ? null : t("parity.community.howTo")}
        </p>
      </div>
    </Card>
  );
}

// ───────────────────────────────────────────────────────────── Discord UI only

const DISCORD_ONLY = [
  "profileStyle",
  "serverTag",
  "activityFeed",
  "joinMode",
  "ageRestricted",
  "rulesScreening",
  "mfa",
  "memberList",
  "template",
  "boostPerks",
] as const;

/** Settings a bot cannot change (docs/discord-parity.md, 🚫 rows). */
export function DiscordOnlyCard() {
  const { t } = useTranslation();
  return (
    <Card aria-labelledby="discord-only-title">
      <CardHeader className="flex-col gap-0.5">
        <CardTitle id="discord-only-title">{t("parity.discordOnly.title")}</CardTitle>
        <CardDescription>{t("parity.discordOnly.description")}</CardDescription>
      </CardHeader>
      <dl className="divide-y">
        {DISCORD_ONLY.map((k) => (
          <div
            key={k}
            className="grid gap-1 px-4 py-2.5 sm:grid-cols-[minmax(0,1fr)_minmax(0,18rem)] sm:gap-4"
          >
            <dt className="text-[13px]">{t(`parity.discordOnly.items.${k}.label`)}</dt>
            <dd className="text-[13px] text-muted-foreground">
              <span className="sr-only">{t("parity.discordOnly.where")}: </span>
              {t(`parity.discordOnly.items.${k}.where`)}
            </dd>
          </div>
        ))}
      </dl>
    </Card>
  );
}

// ───────────────────────────────────────────────────────────── image uploads

const MAX_BYTES = 8 << 20;
const TYPES: Record<ServerAssetKind, string[]> = {
  icon: ["image/png", "image/jpeg", "image/gif", "image/webp"],
  banner: ["image/png", "image/jpeg", "image/gif", "image/webp"],
  splash: ["image/png", "image/jpeg", "image/webp"],
};

/** Image preview: the live Discord URL, else the repository file, else a placeholder. */
function AssetPreview({
  sources,
  kind,
  label,
}: {
  sources: string[];
  kind: ServerAssetKind;
  label: string;
}) {
  const { t } = useTranslation();
  const [failed, setFailed] = useState<string[]>([]);
  const src = sources.find((s) => s && !failed.includes(s));
  return (
    <div
      className={cn(
        "grid shrink-0 place-items-center overflow-hidden border border-border-strong bg-surface-2 text-subtle-foreground",
        kind === "icon" ? "size-16 rounded-[30%]" : "aspect-video w-40 rounded-md",
      )}
    >
      {src ? (
        <img
          src={src}
          alt={label}
          className="size-full object-cover"
          onError={() => setFailed((f) => [...f, src])}
        />
      ) : (
        <span className="flex flex-col items-center gap-1 text-[11px]">
          <ImageOff className="size-4" aria-hidden="true" />
          {t("parity.assets.noImage")}
        </span>
      )}
    </div>
  );
}

/** One server image (icon, banner, splash): preview, declared path and upload. */
export function AssetRow({
  serverKey,
  kind,
  server,
  declared,
  revision,
  canEdit,
}: {
  serverKey: string;
  kind: ServerAssetKind;
  server: ServerStatus;
  /** spec.profile.<kind>, "" when not declared. */
  declared: string;
  /** Active revision id (conflict check). */
  revision: string | undefined;
  canEdit: boolean;
}) {
  const { t } = useTranslation();
  const { client, cache } = useCicord();
  const input = useRef<HTMLInputElement>(null);
  const inputId = useId();
  const [busy, setBusy] = useState(false);
  const [notes, setNotes] = useState<string[]>([]);
  const [pr, setPr] = useState<string>("");
  const label =
    kind === "icon"
      ? t("parity.assets.icon")
      : kind === "banner"
        ? t("parity.assets.banner")
        : t("parity.assets.splash");
  const live =
    kind === "icon" ? server.iconUrl : kind === "banner" ? server.bannerUrl : server.splashUrl;
  const repoUrl = declared
    ? `${client.admin.serverAssetUrl(serverKey, kind)}?v=${encodeURIComponent(revision ?? "")}`
    : "";
  const level = clampLevel(server.boostLevel);
  const required = ASSET_LEVEL[kind];
  const gated = level < required;

  const upload = async (file: File) => {
    if (!TYPES[kind].includes(file.type)) {
      toast.error(
        kind === "splash" ? t("parity.assets.badTypeSplash") : t("parity.assets.badType"),
      );
      return;
    }
    if (file.size > MAX_BYTES) {
      toast.error(t("parity.assets.tooLarge"));
      return;
    }
    setBusy(true);
    setNotes([]);
    setPr("");
    try {
      const res = await client.admin.uploadServerAsset(
        serverKey,
        kind,
        file,
        revision ? { revision } : {},
      );
      setNotes(res.warnings ?? []);
      if (res.mode === "pullRequest") {
        setPr(res.pullRequestUrl);
        toast.info(t("parity.assets.prOpened"), { description: t("parity.assets.prText") });
      } else {
        toast.success(
          res.revision
            ? t("parity.assets.committedRev", { kind: label, rev: res.revision })
            : t("parity.assets.committed", { kind: label }),
        );
      }
      await Promise.all([
        cache.invalidate(keys.server(serverKey)),
        cache.invalidate(keys.status()),
        cache.invalidate(keys.plan(serverKey)),
        cache.invalidate(["admin", "history"]),
        cache.invalidate(["admin", "server", serverKey, "audit"]),
      ]);
    } catch (e) {
      // conflict (stale revision), unsupported_media_type, too_large… are explained by the API
      toastError(e, t("parity.assets.failed"));
    } finally {
      setBusy(false);
      if (input.current) input.current.value = "";
    }
  };

  return (
    <div className="flex flex-col gap-2 px-4 py-3" data-asset={kind}>
      <div className="flex flex-wrap items-center gap-4">
        <AssetPreview sources={[live ?? "", repoUrl]} kind={kind} label={label} />
        <div className="flex min-w-48 flex-1 flex-col gap-0.5">
          <p className="flex items-center gap-2 text-[13px] font-medium">
            {label}
            {required > 0 ? (
              <Badge tone={gated ? "warning" : "violet"}>
                <Rocket aria-hidden="true" />
                {t("parity.boost.level", { level: required })}
              </Badge>
            ) : null}
          </p>
          <p className="truncate text-xs text-muted-foreground">
            {declared ? (
              <>
                {t("parity.assets.declared")} <Mono>{declared}</Mono>
              </>
            ) : (
              t("parity.assets.notDeclared")
            )}
          </p>
          <p className="text-xs text-subtle-foreground">
            {kind === "splash" ? t("parity.assets.acceptSplash") : t("parity.assets.accept")}
          </p>
        </div>
        {canEdit ? (
          <>
            <input
              ref={input}
              id={inputId}
              type="file"
              accept={TYPES[kind].join(",")}
              className="sr-only"
              aria-label={t("parity.assets.chooseLabel", { kind: label.toLowerCase() })}
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) void upload(f);
              }}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => input.current?.click()}
            >
              <Upload />
              {busy
                ? t("parity.assets.uploading")
                : declared
                  ? t("parity.assets.replace")
                  : t("parity.assets.upload")}
            </Button>
          </>
        ) : null}
      </div>
      {gated ? (
        <Callout
          tone="warning"
          title={t("parity.assets.boostGated", { kind: label, required, level })}
          hint={t("parity.assets.boostGatedHint", { required })}
        />
      ) : null}
      {notes.length ? (
        <Callout tone="info" title={t("parity.assets.warnings")}>
          <ul className="list-disc pl-4">
            {notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        </Callout>
      ) : null}
      {pr ? (
        <Callout
          tone="info"
          title={t("parity.assets.prOpened")}
          hint={t("parity.assets.prText")}
          action={
            <Button variant="outline" size="sm" asChild>
              <a href={pr} target="_blank" rel="noreferrer">
                <ExternalLink />
                {t("parity.assets.openPr")}
              </a>
            </Button>
          }
        />
      ) : null}
    </div>
  );
}
