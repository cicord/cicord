/** Where a plugin comes from and what it may do: trust badges and capability chips. */
import type { PluginTrust } from "@cicord/client";
import { FolderGit2, ShieldAlert, ShieldCheck, TriangleAlert } from "lucide-react";
import { useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import { Mono } from "./common.tsx";
import { Badge } from "./ui/badge.tsx";
import { Tip } from "./ui/tooltip.tsx";

/** Official (Cicord or the official registry), Unofficial (any other registry), Local (the repo). */
export function TrustBadge({ trust, className }: { trust: PluginTrust; className?: string }) {
  const { t } = useTranslation();
  if (trust === "official")
    return (
      <Badge tone="brand" className={className} title={t("store.trust.officialTip")}>
        <ShieldCheck aria-hidden="true" />
        {t("store.trust.official")}
      </Badge>
    );
  if (trust === "local")
    return (
      <Badge tone="neutral" className={className} title={t("store.trust.localTip")}>
        <FolderGit2 aria-hidden="true" />
        {t("store.trust.local")}
      </Badge>
    );
  return (
    <Badge
      tone="warning"
      className={cn("border-warning/40", className)}
      title={t("store.trust.unofficialTip")}
    >
      <ShieldAlert aria-hidden="true" />
      {t("store.trust.unofficial")}
    </Badge>
  );
}

/** Capabilities that deserve attention (colour and icon wherever they are listed). */
export const RISK: Record<string, "high" | "medium"> = {
  "discord.messages.manage": "medium",
  "discord.webhooks.manage": "high",
  "discord.members.manage": "medium",
  "discord.members.moderate": "high",
  "discord.roles.assign": "medium",
  "discord.channels.manage": "medium",
  "http.fetch": "medium",
};

/** A capability with its human label, coloured by risk; the raw name in a tooltip. */
export function CapabilityChip({ cap }: { cap: string }) {
  const { t } = useTranslation();
  const risk = RISK[cap];
  const label = t(`plugins.capabilities.${cap.replaceAll(".", "_") as "storage_kv"}`, {
    defaultValue: cap,
  });
  return (
    <Tip content={<Mono>{cap}</Mono>}>
      <span
        tabIndex={0}
        className={cn(
          "inline-flex h-6 items-center gap-1 rounded-md border px-2 text-xs",
          risk === "high"
            ? "border-danger/30 text-danger"
            : risk === "medium"
              ? "border-warning/30 text-warning"
              : "border-border-strong text-foreground/90",
        )}
      >
        {risk ? <TriangleAlert className="size-3" aria-hidden="true" /> : null}
        {label}
      </span>
    </Tip>
  );
}
