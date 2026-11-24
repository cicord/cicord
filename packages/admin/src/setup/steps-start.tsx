/** Wizard steps 1–3: welcome, Discord application, server. */
import { Bot, CheckCircle2, KeyRound, Radio, ServerCog, Sparkles, Users } from "lucide-react";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { ServerAvatar, UserAvatar } from "../components/common.tsx";
import { Callout } from "../components/errors.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Input } from "../components/ui/input.tsx";
import { Label } from "../components/ui/label.tsx";
import { Switch } from "../components/ui/switch.tsx";
import { useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import type { Check, ServerResult, SetupApi, SetupView } from "./api.ts";
import { useAction, usePoll } from "./hooks.ts";
import {
  Checklist,
  ExternalButton,
  Instruction,
  Instructions,
  SetupError,
  SimButton,
  StepFooter,
  StepHeading,
} from "./kit.tsx";

export interface StepProps {
  view: SetupView;
  api: SetupApi;
  onView: (v: SetupView) => void;
}

const PORTAL = "https://discord.com/developers/applications";

// ───────────────────────────────────────────────────────────── welcome

function Choice({
  selected,
  onSelect,
  icon,
  title,
  text,
}: {
  selected: boolean;
  onSelect: () => void;
  icon: ReactNode;
  title: string;
  text: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={cn(
        "flex flex-1 items-start gap-3 rounded-lg border bg-card p-4 text-left outline-none transition-colors hover:bg-surface-hover focus-visible:ring-2 focus-visible:ring-ring",
        selected && "border-brand ring-1 ring-brand",
      )}
    >
      <span className="mt-0.5 text-brand">{icon}</span>
      <span>
        <span className="block text-[13px] font-semibold">{title}</span>
        <span className="mt-0.5 block text-[13px] text-muted-foreground">{text}</span>
      </span>
    </button>
  );
}

export function WelcomeStep({ view, api, onView }: StepProps) {
  const { t } = useTranslation();
  const [intent, setIntent] = useState<"new" | "existing">(view.intent ?? "existing");
  const a = useAction();
  const items = [
    { icon: <Bot className="size-4" />, text: t("setup.welcome.items.bot") },
    { icon: <Users className="size-4" />, text: t("setup.welcome.items.invite") },
    { icon: <ServerCog className="size-4" />, text: t("setup.welcome.items.import") },
    { icon: <Sparkles className="size-4" />, text: t("setup.welcome.items.verify") },
  ];
  return (
    <>
      <StepHeading title={t("setup.welcome.title")} lead={t("setup.welcome.lead")} />
      <h2 className="text-[13px] font-semibold">{t("setup.welcome.willDo")}</h2>
      <ul className="mt-3 grid gap-2 sm:grid-cols-2">
        {items.map((it) => (
          <li key={it.text} className="flex gap-2.5 rounded-lg border bg-surface-2 p-3 text-[13px]">
            <span className="mt-0.5 text-muted-foreground">{it.icon}</span>
            {it.text}
          </li>
        ))}
      </ul>
      <h2 className="mt-8 text-[13px] font-semibold" id="intent-label">
        {t("setup.welcome.question")}
      </h2>
      <div
        className="mt-3 flex flex-col gap-2 sm:flex-row"
        role="radiogroup"
        aria-labelledby="intent-label"
      >
        <Choice
          selected={intent === "existing"}
          onSelect={() => setIntent("existing")}
          icon={<ServerCog className="size-5" />}
          title={t("setup.welcome.existing")}
          text={t("setup.welcome.existingText")}
        />
        <Choice
          selected={intent === "new"}
          onSelect={() => setIntent("new")}
          icon={<Sparkles className="size-5" />}
          title={t("setup.welcome.new")}
          text={t("setup.welcome.newText")}
        />
      </div>
      {intent === "new" ? (
        <Callout tone="info" className="mt-4" title={t("setup.welcome.new")}>
          <p>{t("setup.welcome.newSteps")}</p>
          <div className="mt-2">
            <ExternalButton href="https://discord.com/channels/@me">
              {t("setup.welcome.openDiscord")}
            </ExternalButton>
          </div>
        </Callout>
      ) : null}
      {a.error ? <SetupError className="mt-4" error={a.error} /> : null}
      <StepFooter>
        <Button
          loading={a.pending}
          onClick={() => void a.run(() => api.intent(intent)).then((v) => v && onView(v))}
        >
          {t("setup.welcome.start")}
        </Button>
      </StepFooter>
    </>
  );
}

// ───────────────────────────────────────────────────────────── Discord application

function IntentsPanel({ view, api, onView }: StepProps) {
  const { t } = useTranslation();
  const bot = view.bot!;
  const c = bot.intents;
  const missing = ((c.data?.["missing"] as string[] | undefined) ?? []).filter(Boolean);
  const a = useAction();
  return (
    <section className="mt-6 rounded-lg border bg-card p-4" aria-labelledby="intents-title">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="intents-title" className="text-[13px] font-semibold">
          {t("setup.discord.intentsTitle")}
        </h2>
        <Badge tone={c.status === "ok" ? "success" : "warning"}>
          {t(`setup.checks.status.${c.status}`)}
        </Badge>
      </div>
      <p className="mt-1 text-[13px] text-muted-foreground">{t("setup.discord.intentsLead")}</p>
      <p className="mt-3 text-[13px]" role="status">
        {c.message}
      </p>
      {c.hint && c.status !== "ok" ? (
        <p className="mt-1 text-[13px] text-muted-foreground">{c.hint}</p>
      ) : null}
      {missing.length ? (
        <div className="mt-3 text-[13px]">
          <p>{t("setup.discord.intentToggles")}</p>
          <ul className="mt-1.5 flex flex-wrap gap-1.5">
            {missing.map((m) => (
              <li key={m}>
                <Badge tone="warning">
                  {t(`setup.discord.toggle.${m}` as "setup.discord.toggle.ServerMembers", {
                    defaultValue: m,
                  })}
                </Badge>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <div className="mt-3 flex flex-wrap gap-2">
        {c.status !== "ok" ? (
          <ExternalButton href={bot.portalUrl}>{t("setup.discord.openBotPage")}</ExternalButton>
        ) : null}
        {view.simulator && c.status !== "ok" ? (
          <SimButton
            loading={a.pending}
            onClick={() =>
              void a
                .run(async () => {
                  await api.sim.intents(true);
                  return api.recheckDiscord();
                })
                .then((v) => v && onView(v))
            }
          >
            {t("setup.discord.simIntents")}
          </SimButton>
        ) : null}
        <Button
          variant="ghost"
          size="sm"
          loading={a.pending}
          onClick={() => void a.run(() => api.recheckDiscord()).then((v) => v && onView(v))}
        >
          {t("setup.recheck")}
        </Button>
      </div>
      {a.error ? <SetupError className="mt-3" error={a.error} /> : null}
    </section>
  );
}

export function DiscordStep(props: StepProps) {
  const { view, api, onView } = props;
  const { t } = useTranslation();
  const [token, setToken] = useState("");
  const [editing, setEditing] = useState(!view.bot);
  const [simNote, setSimNote] = useState(false);
  const a = useAction();
  const next = useAction();
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!token.trim()) return;
    const v = await a.run(() => api.submitToken(token));
    if (v) {
      setToken("");
      setEditing(false);
      onView(v);
    }
  };
  const bot = view.bot;
  return (
    <>
      <StepHeading title={t("setup.discord.title")} lead={t("setup.discord.lead")} />
      {editing ? (
        <>
          <Instructions>
            <Instruction n={1}>
              <p>{t("setup.discord.step1")}</p>
              <div className="mt-2">
                <ExternalButton href={PORTAL}>{t("setup.openPortal")}</ExternalButton>
              </div>
            </Instruction>
            <Instruction n={2}>{t("setup.discord.step2")}</Instruction>
            <Instruction n={3}>{t("setup.discord.step3")}</Instruction>
          </Instructions>
          {view.simulator ? (
            <Callout tone="info" className="mt-5" title={t("setup.simulator")}>
              <p>{t("setup.simulatorHint")}</p>
              <div className="mt-2">
                <SimButton
                  onClick={async () => {
                    const r = await a.run(() => api.sim.createBot());
                    if (r) {
                      setToken(r.token);
                      setSimNote(true);
                    }
                  }}
                >
                  {t("setup.discord.simCreate")}
                </SimButton>
              </div>
              {simNote ? (
                <p className="mt-2 text-muted-foreground">{t("setup.discord.simCreated")}</p>
              ) : null}
            </Callout>
          ) : null}
          <form className="mt-5 flex flex-col gap-2" onSubmit={submit}>
            <Label htmlFor="bot-token">{t("setup.discord.tokenLabel")}</Label>
            <div className="flex flex-col gap-2 sm:flex-row">
              <Input
                id="bot-token"
                type="password"
                autoComplete="off"
                spellCheck={false}
                className="font-mono text-[13px]"
                placeholder={t("setup.discord.tokenPlaceholder")}
                value={token}
                onChange={(e) => setToken(e.target.value)}
                aria-describedby="bot-token-help"
                aria-invalid={a.error ? true : undefined}
              />
              <Button type="submit" loading={a.pending} disabled={!token.trim()}>
                <KeyRound />
                {t("setup.discord.submit")}
              </Button>
            </div>
            <p id="bot-token-help" className="text-xs text-muted-foreground">
              {t("setup.discord.tokenHelp")}
            </p>
          </form>
          {a.error ? <SetupError className="mt-4" error={a.error} /> : null}
        </>
      ) : null}
      {bot && !editing ? (
        <>
          <section
            className="flex flex-wrap items-center gap-3 rounded-lg border bg-card p-4"
            aria-label={t("setup.discord.connected")}
          >
            <UserAvatar
              name={bot.username}
              {...(bot.avatarUrl ? { url: bot.avatarUrl } : {})}
              size={40}
            />
            <div className="min-w-[12rem] flex-1">
              <p className="flex items-center gap-1.5 text-[14px] font-semibold">
                <CheckCircle2 className="size-4 shrink-0 text-success" aria-hidden="true" />
                {t("setup.discord.connected")}: {bot.username}
              </p>
              <p className="text-xs text-muted-foreground">
                {t("setup.discord.application")} {bot.applicationName} ·{" "}
                {t("setup.discord.applicationId")}{" "}
                <span className="font-mono">{bot.applicationId}</span>
              </p>
              <p className="text-xs text-muted-foreground">
                {t("setup.discord.stored", { ref: bot.secretRef })}
              </p>
            </div>
            <Button
              variant="ghost"
              size="sm"
              className="w-full sm:w-auto"
              onClick={() => setEditing(true)}
            >
              {t("setup.discord.replace")}
            </Button>
          </section>
          <IntentsPanel {...props} />
        </>
      ) : null}
      {next.error ? <SetupError className="mt-4" error={next.error} /> : null}
      <StepFooter
        onBack={() => void next.run(() => api.step("welcome")).then((v) => v && onView(v))}
      >
        {bot && !editing ? (
          <Button
            loading={next.pending}
            onClick={() => void next.run(() => api.step("server")).then((v) => v && onView(v))}
          >
            {bot.intents.status === "ok" ? t("setup.continue") : t("setup.continueAnyway")}
          </Button>
        ) : null}
      </StepFooter>
    </>
  );
}

// ───────────────────────────────────────────────────────────── server

export function ServerStep({ view, api, onView }: StepProps) {
  const { t } = useTranslation();
  const [result, setResult] = useState<ServerResult | null>(null);
  const [low, setLow] = useState(false);
  const a = useAction();
  const next = useAction();
  const servers = usePoll((signal) => api.servers(signal), 2500);
  const list = servers.data ?? [];
  const select = async (id: string) => {
    const r = await a.run(() => api.selectServer(id));
    if (r) {
      setResult(r);
      onView(r.view);
    }
  };
  const recheck = async () => {
    const r = await a.run(() => api.checkServer());
    if (r) setResult(r);
  };
  // Resumed on this step (reload, restart): check the selected server right away.
  useEffect(() => {
    if (view.server && !result) void recheck();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once when the wizard resumes on this step
  }, []);
  const checks: Check[] | undefined = result?.checks;
  const selectedId = view.server?.id;
  const ok = result?.ok ?? false;
  return (
    <>
      <StepHeading title={t("setup.server.title")} lead={t("setup.server.lead")} />
      <section className="rounded-lg border bg-card p-4">
        <div className="flex flex-wrap items-center gap-2">
          {view.inviteUrl ? (
            <ExternalButton href={view.inviteUrl} variant="discord" size="default">
              {t("setup.server.invite")}
            </ExternalButton>
          ) : null}
          {view.simulator ? (
            <>
              <SimButton
                onClick={() => void a.run(() => api.sim.invite(low)).then(() => servers.refresh())}
              >
                {t("setup.server.simInvite")}
              </SimButton>
              <label className="flex items-center gap-2 text-xs text-muted-foreground">
                <Switch
                  checked={low}
                  onCheckedChange={setLow}
                  aria-label={t("setup.server.simLow")}
                />
                {t("setup.server.simLow")}
              </label>
            </>
          ) : null}
        </div>
        <p className="mt-2 text-xs text-muted-foreground">{t("setup.server.inviteDetails")}</p>
        <p className="mt-1 text-xs text-muted-foreground">{t("setup.server.noManage")}</p>
      </section>

      <section className="mt-6" aria-labelledby="servers-title">
        <div className="flex items-center justify-between gap-2">
          <h2 id="servers-title" className="text-[13px] font-semibold">
            {t("setup.server.listTitle")}
          </h2>
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground" role="status">
            <Radio className="size-3.5 animate-pulse text-success" aria-hidden="true" />
            {t("setup.server.watching")}
          </span>
        </div>
        {servers.error ? <SetupError className="mt-2" error={servers.error} /> : null}
        <ul className="mt-2 divide-y rounded-lg border bg-card">
          {list.length === 0 ? (
            <li className="px-4 py-6 text-center text-[13px] text-muted-foreground">
              {t("setup.server.none")}
            </li>
          ) : (
            list.map((s) => (
              <li key={s.id} className="flex items-center gap-3 px-4 py-3">
                <ServerAvatar name={s.name} {...(s.iconUrl ? { url: s.iconUrl } : {})} size={32} />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px] font-medium">{s.name}</p>
                  <p className="font-mono text-xs text-subtle-foreground">{s.id}</p>
                </div>
                {selectedId === s.id ? (
                  <Badge tone="brand">{t("setup.server.selected")}</Badge>
                ) : (
                  <Button
                    size="sm"
                    variant="outline"
                    loading={a.pending}
                    onClick={() => select(s.id)}
                  >
                    {t("setup.server.select")}
                  </Button>
                )}
              </li>
            ))
          )}
        </ul>
      </section>

      {view.server ? (
        <section className="mt-6" aria-labelledby="checks-title">
          <div className="flex items-center justify-between gap-2">
            <h2 id="checks-title" className="text-[13px] font-semibold">
              {t("setup.server.checksTitle", { name: view.server.name })}
            </h2>
            <Button variant="ghost" size="sm" loading={a.pending} onClick={recheck}>
              {t("setup.recheck")}
            </Button>
          </div>
          <div className="mt-2">
            {checks ? (
              <Checklist
                checks={checks}
                label={t("setup.server.checksTitle", { name: view.server.name })}
                extra={
                  view.simulator
                    ? {
                        hierarchy: checks.some(
                          (c) => c.id === "hierarchy" && c.status === "fail",
                        ) ? (
                          <SimButton
                            onClick={() => void a.run(() => api.sim.raiseRole()).then(recheck)}
                          >
                            {t("setup.server.simRaise")}
                          </SimButton>
                        ) : null,
                      }
                    : undefined
                }
              />
            ) : (
              <Button variant="outline" size="sm" onClick={recheck} loading={a.pending}>
                {t("setup.recheck")}
              </Button>
            )}
          </div>
        </section>
      ) : null}
      {a.error ? <SetupError className="mt-4" error={a.error} /> : null}
      {next.error ? <SetupError className="mt-4" error={next.error} /> : null}
      <StepFooter
        onBack={() => void next.run(() => api.step("discord")).then((v) => v && onView(v))}
      >
        <Button
          disabled={!view.server || !ok}
          loading={next.pending}
          onClick={() => void next.run(() => api.step("repository")).then((v) => v && onView(v))}
        >
          {t("setup.continue")}
        </Button>
      </StepFooter>
    </>
  );
}
