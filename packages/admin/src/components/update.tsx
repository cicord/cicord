/** "Update available" badge of Store plugins and plugin instances (https://cicord.dev/docs/reference/http-api/store/). */
import type { PluginUpdate } from "@cicord/client";
import { ArrowUpCircle } from "lucide-react";
import { useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import { Link } from "../router.tsx";
import { Callout } from "./errors.tsx";
import { Badge, badgeVariants } from "./ui/badge.tsx";

/**
 * "Update to 0.2.1": the same words everywhere an update is offered (Store table, Plugins
 * table, Store page, instance page). With `to` (admins), a link to the update review.
 */
export function UpdateBadge({
  update,
  to,
  className,
}: {
  update: PluginUpdate;
  /** The update review (storeUpdatePath); absent when the viewer cannot update. */
  to?: string;
  className?: string;
}) {
  const { t } = useTranslation();
  const tip = t("store.update.badgeTip", {
    current: update.current,
    latest: update.latest,
    level: t(`store.update.level.${update.level}`),
  });
  const tone = update.level === "major" || update.modified ? "warning" : "info";
  const label = t("store.update.badge", { version: update.latest });
  if (to)
    return (
      <Link
        to={to}
        title={tip}
        className={cn(
          badgeVariants({ tone }),
          "outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring",
          className,
        )}
      >
        <ArrowUpCircle aria-hidden="true" />
        {label}
      </Link>
    );
  return (
    <Badge tone={tone} title={tip} className={className}>
      <ArrowUpCircle aria-hidden="true" />
      {label}
    </Badge>
  );
}

/**
 * What a Store change (install, update, uninstall) could not keep up to date, e.g. the
 * custom portal's route types when `cicord portal typegen` failed: the change was saved.
 */
export function WriteWarnings({ warnings }: { warnings?: string[] | undefined }) {
  const { t } = useTranslation();
  if (!warnings?.length) return null;
  return (
    <Callout tone="warning" title={t("store.warnings.title")}>
      <p>{t("store.warnings.hint")}</p>
      <ul className="list-disc pl-5" aria-label={t("store.warnings.title")}>
        {warnings.map((w) => (
          <li key={w}>{w}</li>
        ))}
      </ul>
    </Callout>
  );
}
