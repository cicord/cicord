/** Server step, "Inspect first": the read-only report of the chosen server. */
import { CheckCircle2, Eye, EyeOff, FileWarning, KeyRound, ShieldCheck } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { Callout } from "../components/errors.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { useTranslation } from "../i18n/index.ts";
import type { ChannelAccess, InspectResult, SetupApi, SetupView } from "./api.ts";
import { useAction } from "./hooks.ts";
import { ExternalButton, SetupError, SimButton } from "./kit.tsx";

function Section({
  icon,
  title,
  children,
}: {
  icon: ReactNode;
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="border-t px-4 py-3 first:border-t-0" aria-label={title}>
      <h3 className="flex items-center gap-1.5 text-[13px] font-semibold">
        <span className="text-muted-foreground">{icon}</span>
        {title}
      </h3>
      <div className="mt-1.5 text-[13px]">{children}</div>
    </section>
  );
}

/** Channels grouped by category: "Community: #rules, #announcements". */
function ChannelGroups({ all, shown }: { all: ChannelAccess[]; shown: ChannelAccess[] }) {
  const { t } = useTranslation();
  const label = (c: ChannelAccess) =>
    `${c.type === "category" || c.type === "voice" || c.type === "stage" ? "" : "#"}${c.name}`;
  const cats = all.filter((c) => c.type === "category");
  const groups: { id: string; name: string; items: ChannelAccess[] }[] = [
    { id: "", name: t("setup.inspect.noCategory"), items: [] },
    ...cats.map((c) => ({ id: c.id, name: c.name, items: [] as ChannelAccess[] })),
  ];
  for (const c of shown) {
    if (c.type === "category") continue;
    groups.find((g) => g.id === (c.categoryId ?? ""))?.items.push(c);
  }
  const shownCats = new Set(shown.filter((c) => c.type === "category").map((c) => c.id));
  return (
    <ul className="space-y-1">
      {groups
        .filter((g) => g.items.length > 0 || shownCats.has(g.id))
        .map((g) => (
          <li key={g.id || "top"}>
            <span className="font-medium">{g.name}</span>
            {cats.find((c) => c.id === g.id)?.private ? (
              <Badge tone="neutral" className="ml-1.5">
                {t("setup.inspect.private")}
              </Badge>
            ) : null}
            {g.items.length ? <span className="text-muted-foreground">: </span> : null}
            {g.items.map((c, i) => (
              <span key={c.id}>
                {i > 0 ? ", " : ""}
                <span className="font-mono text-[12.5px]">{label(c)}</span>
                {c.private ? (
                  <span className="text-muted-foreground"> ({t("setup.inspect.private")})</span>
                ) : null}
              </span>
            ))}
          </li>
        ))}
    </ul>
  );
}

/** Runs the inspection and shows what the bot sees, what it could not read and what
 *  applying changes would need. Nothing is changed on Discord. */
export function InspectPanel({
  view,
  api,
  onUpgraded,
}: {
  view: SetupView;
  api: SetupApi;
  onUpgraded: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [res, setRes] = useState<InspectResult | null>(null);
  const a = useAction();
  const run = async () => {
    const r = await a.run(() => api.inspect());
    if (r) setRes(r);
  };
  const serverId = view.server?.id;
  useEffect(() => {
    if (serverId) void run();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-runs when another server is picked
  }, [serverId]);
  const rep = res?.report;
  const all = rep ? [...rep.categories, ...rep.channels] : [];
  const visible = all.filter((c) => c.visible);
  const hidden = all.filter((c) => !c.visible);
  const count = (cs: ChannelAccess[]) => ({
    categories: cs.filter((c) => c.type === "category").length,
    channels: cs.filter((c) => c.type !== "category").length,
  });
  return (
    <section className="mt-6" aria-labelledby="inspect-title">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="inspect-title" className="text-[13px] font-semibold">
          {t("setup.inspect.title", { name: view.server?.name ?? "" })}
        </h2>
        <Button variant="ghost" size="sm" loading={a.pending} onClick={() => void run()}>
          {rep ? t("setup.inspect.rerun") : t("setup.inspect.run")}
        </Button>
      </div>
      <p className="mt-1 text-xs text-muted-foreground">{t("setup.inspect.lead")}</p>
      {a.error ? <SetupError className="mt-3" error={a.error} /> : null}
      {!rep && a.pending ? (
        <p className="mt-3 text-[13px] text-muted-foreground" role="status">
          {t("setup.inspect.running")}
        </p>
      ) : null}
      {rep && res ? (
        <>
          <p
            className="mt-3 flex items-center gap-1.5 text-[13px] font-medium text-success"
            role="status"
          >
            <ShieldCheck className="size-4" aria-hidden="true" />
            {t("setup.inspect.nothingChanged", { reads: rep.reads, writes: rep.writes })}
          </p>
          <div className="mt-3 rounded-lg border bg-card">
            <Section icon={<KeyRound className="size-4" />} title={t("setup.inspect.access")}>
              <p>
                {rep.administrator
                  ? t("setup.inspect.admin")
                  : t("setup.inspect.permissions", { names: rep.rolePermissions.join(", ") })}
              </p>
              <p className="mt-1 text-muted-foreground">{rep.hierarchy.message}</p>
            </Section>
            <Section icon={<Eye className="size-4" />} title={t("setup.inspect.visible")}>
              <p className="mb-1 text-muted-foreground">
                {t("setup.inspect.visibleCount", count(visible))}
              </p>
              <ChannelGroups all={all} shown={visible} />
            </Section>
            <Section icon={<EyeOff className="size-4" />} title={t("setup.inspect.hidden")}>
              {rep.administrator ? (
                <p>{t("setup.inspect.hiddenNone")}</p>
              ) : (
                <>
                  {hidden.length > 0 ? (
                    <>
                      <p className="mb-1 text-muted-foreground">{t("setup.inspect.hiddenLead")}</p>
                      <ChannelGroups all={all} shown={hidden} />
                    </>
                  ) : null}
                  {rep.unlisted.length > 0 ? (
                    <p className="mt-1">
                      {t("setup.inspect.unlisted", {
                        count: rep.unlisted.length,
                        refs: rep.unlisted.flatMap((u) => u.referencedBy).join(", "),
                      })}
                    </p>
                  ) : null}
                  <p className="mt-1 text-muted-foreground">{t("setup.inspect.omitted")}</p>
                </>
              )}
            </Section>
            <Section
              icon={<FileWarning className="size-4" />}
              title={t("setup.inspect.unreadable")}
            >
              {rep.unreadable.length === 0 ? (
                <p>{t("setup.inspect.unreadableNone")}</p>
              ) : (
                <>
                  <p className="mb-1 text-muted-foreground">{t("setup.inspect.unreadableLead")}</p>
                  <ul>
                    {rep.unreadable.map((u) => (
                      <li key={u.part}>
                        {u.name}
                        {u.needs ? (
                          <>
                            {" — "}
                            <span className="font-mono text-[12.5px]">{u.needs}</span>
                          </>
                        ) : null}
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </Section>
            <Section icon={<KeyRound className="size-4" />} title={t("setup.inspect.needsTitle")}>
              {rep.needs.length === 0 ? (
                <p>{t("setup.inspect.needsNone")}</p>
              ) : (
                <ul className="space-y-1.5" aria-label={t("setup.inspect.needsTitle")}>
                  {rep.needs.map((n) => (
                    <li key={n.permission}>
                      <span className="font-mono text-[12.5px] font-medium">{n.permission}</span>
                      <span className="text-muted-foreground"> — {n.why}</span>
                      <p className="text-xs text-subtle-foreground">
                        {t("setup.inspect.neededBy", { paths: n.sources.join(", ") })}
                      </p>
                    </li>
                  ))}
                </ul>
              )}
            </Section>
            <Section icon={<CheckCircle2 className="size-4" />} title={t("setup.inspect.plan")}>
              <p>
                {res.plan.changes.length === 0
                  ? t("setup.inspect.planEmpty")
                  : rep.unlisted.length > 0
                    ? t("setup.inspect.planIncomplete", { count: res.plan.changes.length })
                    : t("setup.inspect.planChanges", { count: res.plan.changes.length })}
              </p>
            </Section>
          </div>
          {!rep.ready ? (
            <>
              <Callout tone="info" className="mt-4" title={t("setup.inspect.stopTitle")}>
                <p>{t("setup.inspect.stopText")}</p>
              </Callout>
              <Callout tone="warning" className="mt-3" title={t("setup.inspect.upgradeTitle")}>
                <p>{t("setup.inspect.upgradeText")}</p>
                <div className="mt-2 flex flex-wrap gap-2">
                  {rep.upgradeInviteUrl ? (
                    <ExternalButton href={rep.upgradeInviteUrl}>
                      {t("setup.inspect.upgradeExact")}
                    </ExternalButton>
                  ) : null}
                  {rep.adminInviteUrl ? (
                    <ExternalButton href={rep.adminInviteUrl}>
                      {t("setup.inspect.upgradeAdmin")}
                    </ExternalButton>
                  ) : null}
                  {view.simulator ? (
                    <SimButton
                      loading={a.pending}
                      onClick={() =>
                        void a.run(async () => {
                          await api.sim.grantAdmin();
                          await onUpgraded();
                          await run();
                        })
                      }
                    >
                      {t("setup.inspect.simGrant")}
                    </SimButton>
                  ) : null}
                  <Button
                    variant="ghost"
                    size="sm"
                    loading={a.pending}
                    onClick={() =>
                      void a.run(async () => {
                        await onUpgraded();
                        await run();
                      })
                    }
                  >
                    {t("setup.recheck")}
                  </Button>
                </div>
              </Callout>
            </>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
