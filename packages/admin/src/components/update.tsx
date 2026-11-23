/** "Update available" badge of Store plugins and plugin instances (spec/api.md §11). */
import type { PluginUpdate } from "@codi/client";
import { ArrowUpCircle } from "lucide-react";
import { useTranslation } from "../i18n/index.ts";
import { Badge } from "./ui/badge.tsx";

export function UpdateBadge({ update, className }: { update: PluginUpdate; className?: string }) {
  const { t } = useTranslation();
  const tip = t("store.update.badgeTip", {
    current: update.current,
    latest: update.latest,
    level: t(`store.update.level.${update.level}`),
  });
  return (
    <Badge
      tone={update.level === "major" || update.modified ? "warning" : "info"}
      title={tip}
      className={className}
    >
      <ArrowUpCircle aria-hidden="true" />
      {t("store.update.badge", { version: update.latest })}
    </Badge>
  );
}
