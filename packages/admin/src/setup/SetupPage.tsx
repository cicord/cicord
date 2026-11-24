/**
 * The setup wizard (https://cicord.dev/docs/getting-started/setup-wizard/): a full-page route outside the console
 * shell, like Login. It opens with the one-time setup link printed in the runtime logs
 * (/admin/setup?token=…), exchanged for a setup session; the step lives on the runtime,
 * so the wizard resumes where it was after a reload or a restart.
 */
import { CicordApiError } from "@cicord/client";
import { useCicord } from "@cicord/react";
import { Check, CheckCircle2, KeyRound, Loader2, Moon, Sun } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Input } from "../components/ui/input.tsx";
import { Label } from "../components/ui/label.tsx";
import { BrandMark } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { cn } from "../lib/utils.ts";
import { href, useLocation } from "../router.tsx";
import { useTheme } from "../shell/theme.tsx";
import { setupApi, tokenFrom, type SetupStatus, type SetupView, type StepId } from "./api.ts";
import { SetupError } from "./kit.tsx";
import { DoneStep, LoginStep, PluginStep, RepositoryStep, VerifyStep } from "./steps-end.tsx";
import { DiscordStep, ServerStep, WelcomeStep, type StepProps } from "./steps-start.tsx";

const STEP_COMPONENT: Record<StepId, (p: StepProps) => React.JSX.Element> = {
  welcome: WelcomeStep,
  discord: DiscordStep,
  server: ServerStep,
  repository: RepositoryStep,
  login: LoginStep,
  verify: VerifyStep,
  plugin: PluginStep,
  done: DoneStep,
};

function Frame({ children, simulator }: { children: React.ReactNode; simulator?: boolean }) {
  const { t } = useTranslation();
  const { theme, toggle } = useTheme();
  return (
    <div className="min-h-svh bg-background">
      <header className="flex h-(--header-h) items-center gap-2 border-b px-4 md:px-6">
        <BrandMark size={22} />
        <span className="text-[16px] font-semibold tracking-tight">cicord</span>
        <span className="text-[14px] text-subtle-foreground">{t("setup.brand")}</span>
        {simulator ? (
          <Badge tone="violet" className="ml-2">
            {t("setup.simulator")}
          </Badge>
        ) : null}
        <Button
          variant="ghost"
          size="icon-sm"
          className="ml-auto"
          onClick={toggle}
          aria-label={theme === "dark" ? t("header.lightTheme") : t("header.darkTheme")}
        >
          {theme === "dark" ? <Sun /> : <Moon />}
        </Button>
      </header>
      {children}
    </div>
  );
}

function Stepper({ view }: { view: SetupView }) {
  const { t } = useTranslation();
  const current = view.steps.indexOf(view.step);
  return (
    <nav aria-label={t("setup.stepper")} className="md:sticky md:top-6">
      <p className="mb-3 text-xs text-muted-foreground md:hidden">
        {t("setup.stepOf", { n: current + 1, total: view.steps.length })} ·{" "}
        {t(`setup.steps.${view.step}`)}
      </p>
      <ol className="hidden flex-col gap-1 md:flex">
        {view.steps.map((s, i) => {
          const done = i < current || view.completed;
          const active = i === current && !view.completed;
          return (
            <li
              key={s}
              aria-current={active ? "step" : undefined}
              className={cn(
                "flex items-center gap-3 rounded-md px-2 py-1.5",
                active && "bg-surface-2",
              )}
            >
              <span
                className={cn(
                  "inline-grid size-6 shrink-0 place-items-center rounded-full border text-[11px] font-semibold",
                  done && "border-transparent bg-success-soft text-success",
                  active && "border-brand bg-brand text-brand-foreground",
                  !done && !active && "border-border-strong text-subtle-foreground",
                )}
                aria-hidden="true"
              >
                {done ? <Check className="size-3.5" /> : i + 1}
              </span>
              <span className="min-w-0">
                <span
                  className={cn(
                    "block truncate text-[13px] font-medium",
                    !done && !active && "text-muted-foreground",
                  )}
                >
                  {t(`setup.steps.${s}`)}
                </span>
                <span className="block truncate text-xs text-subtle-foreground">
                  {t(`setup.stepHints.${s}`)}
                </span>
              </span>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

function AccessCard({
  onToken,
  error,
  pending,
}: {
  onToken: (token: string) => void;
  error: unknown;
  pending: boolean;
}) {
  const { t } = useTranslation();
  const [value, setValue] = useState("");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (value.trim()) onToken(tokenFrom(value));
  };
  return (
    <main className="mx-auto w-full max-w-[460px] px-4 py-16">
      <KeyRound className="size-6 text-brand" aria-hidden="true" />
      <h1 className="mt-4 text-xl font-semibold tracking-[-0.01em]">{t("setup.access.title")}</h1>
      <p className="mt-2 text-[13px] leading-relaxed text-muted-foreground">
        {t("setup.access.text", { example: "http://localhost:7420/admin/setup?token=…" })}
      </p>
      <form className="mt-6 flex flex-col gap-2" onSubmit={submit}>
        <Label htmlFor="setup-token">{t("setup.access.paste")}</Label>
        <Input
          id="setup-token"
          autoComplete="off"
          spellCheck={false}
          className="font-mono text-[13px]"
          placeholder={t("setup.access.placeholder")}
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
        <Button type="submit" className="mt-1" loading={pending} disabled={!value.trim()}>
          {t("setup.access.submit")}
        </Button>
      </form>
      {error ? <SetupError className="mt-4" error={error} /> : null}
      <p className="mt-6 text-xs text-muted-foreground">{t("setup.access.restart")}</p>
    </main>
  );
}

export function SetupPage() {
  const { t } = useTranslation();
  const { client } = useCicord();
  const api = useMemo(() => setupApi(client), [client]);
  const { search } = useLocation();
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [pending, setPending] = useState(false);
  const started = useRef(false);

  const exchange = async (token: string) => {
    setPending(true);
    setError(null);
    try {
      setStatus(await api.exchange(token));
    } catch (e) {
      // Already signed in (link opened twice)? Keep the session.
      const st = await api.status().catch(() => null);
      if (st?.authenticated) setStatus(st);
      else {
        setStatus(st);
        setError(e);
      }
    } finally {
      setPending(false);
    }
  };

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const token = search.get("token");
    if (token) {
      // Never keep the one-time token in the address bar or the history.
      window.history.replaceState(null, "", href("/setup"));
      void exchange(token);
    } else {
      api
        .status()
        .then(setStatus)
        .catch((e: unknown) => setError(e));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- status is fetched once on mount; later refreshes go through the wizard actions
  }, []);

  if (search.get("login") === "done")
    return (
      <Frame>
        <main className="mx-auto max-w-[460px] px-4 py-16 text-center">
          <CheckCircle2 className="mx-auto size-8 text-success" aria-hidden="true" />
          <h1 className="mt-4 text-lg font-semibold">{t("setup.login.returnTitle")}</h1>
          <p className="mt-2 text-[13px] text-muted-foreground">{t("setup.login.returnText")}</p>
        </main>
      </Frame>
    );

  if (!status && !error)
    return (
      <Frame>
        <div className="grid min-h-[60svh] place-items-center" role="status">
          <Loader2
            className="size-5 animate-spin text-muted-foreground"
            aria-label={t("setup.access.exchanging")}
          />
        </div>
      </Frame>
    );

  if (status?.completed)
    return (
      <Frame>
        <main className="mx-auto max-w-[460px] px-4 py-16">
          <CheckCircle2 className="size-6 text-success" aria-hidden="true" />
          <h1 className="mt-4 text-xl font-semibold">{t("setup.completed.title")}</h1>
          <p className="mt-2 text-[13px] text-muted-foreground">{t("setup.completed.text")}</p>
          <Button asChild className="mt-6">
            <a href={href("/")}>{t("setup.completed.open")}</a>
          </Button>
        </main>
      </Frame>
    );

  const view = status?.view;
  if (!status?.authenticated || !view)
    return (
      <Frame>
        <AccessCard
          onToken={(tok) => void exchange(tok)}
          pending={pending}
          error={
            error && !(error instanceof CicordApiError && error.code === "setup_required")
              ? error
              : null
          }
        />
      </Frame>
    );

  const Step = STEP_COMPONENT[view.step] ?? WelcomeStep;
  const onView = (v: SetupView) => {
    setStatus((s) => (s ? { ...s, view: v, completed: v.completed } : s));
    window.scrollTo({ top: 0 });
  };
  return (
    <Frame simulator={view.simulator}>
      <div className="mx-auto grid w-full max-w-5xl gap-6 px-4 py-6 md:grid-cols-[220px_minmax(0,1fr)] md:gap-10 md:px-6 md:py-10">
        <Stepper view={view} />
        <main id="main" className="min-w-0 rounded-xl border bg-card/40 p-4 md:p-8">
          <Step key={view.step} view={view} api={api} onView={onView} />
        </main>
      </div>
    </Frame>
  );
}
