import {
  CodiApiError,
  type OnboardingAnswers,
  type OnboardingCompleteRequest,
  type OnboardingCompleteResponse,
  type OnboardingOption,
  type OnboardingStep,
  type OnboardingSteps,
} from "@codi/client";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { usePluginMutation, usePluginQuery, usePortalPlugin, type PluginRef } from "./hooks.ts";

export type OnboardingStatus =
  /** Looking up the plugin instance or loading the steps. */
  | "loading"
  /** The server's portal exposes no onboarding plugin. */
  | "unavailable"
  /** The viewer is not logged in (401) — show a login button. */
  | "unauthenticated"
  /** Loading failed (not a member, network…). */
  | "error"
  /** Picking roles. */
  | "ready"
  | "submitting"
  /** `POST /complete` succeeded: show `result`. */
  | "complete";

export interface OnboardingFlowState {
  status: OnboardingStatus;
  error: unknown;
  /** Raw `GET /steps` response. */
  data: OnboardingSteps | undefined;
  /** Resolved plugin instance ({bot, instance, server}). */
  plugin: PluginRef | undefined;

  steps: OnboardingStep[];
  /** Index of the current step. */
  index: number;
  step: OnboardingStep | undefined;
  total: number;
  isFirst: boolean;
  isLast: boolean;
  /** 0 → 1, counting completed steps. */
  progress: number;

  /** Current picks: step id → role keys (initialised from the member's roles). */
  answers: OnboardingAnswers;
  selected: (stepId?: string) => string[];
  isSelected: (role: string, stepId?: string) => boolean;
  /** Toggles an option; on single-choice steps, picking replaces the previous choice. */
  toggle: (role: string, stepId?: string) => void;
  setAnswer: (stepId: string, roles: string[]) => void;

  /** The current step is valid (required steps need one pick). */
  canContinue: boolean;
  /** Server-side validation messages by step id (from 422 diagnostics), plus local ones. */
  stepErrors: Record<string, string>;

  next: () => void;
  back: () => void;
  goTo: (index: number) => void;
  /** Sends the answers. Resolves the response, or undefined on error (see `error`). */
  submit: () => Promise<OnboardingCompleteResponse | undefined>;
  result: OnboardingCompleteResponse | undefined;
  /** Back to the first step, keeping the current answers. */
  restart: () => void;
  /** Reloads the steps. */
  reload: () => void;
}

export interface UseOnboardingOptions {
  /** Server key (defaults to the provider's). */
  server?: string;
  /** Plugin instance; by default the first portal plugin named `plugin`. */
  bot?: string;
  instance?: string;
  /** Plugin name to look for. Default "onboarding". */
  plugin?: string;
  onComplete?: (result: OnboardingCompleteResponse) => void;
}

function optionRoles(step: OnboardingStep): Set<string> {
  return new Set(step.options.map((o: OnboardingOption) => o.role));
}

/** State machine behind `<OnboardingFlow>` (headless: render whatever you want). */
export function useOnboarding(options: UseOnboardingOptions = {}): OnboardingFlowState {
  const found = usePortalPlugin(options.plugin ?? "onboarding", {
    ...(options.server ? { server: options.server } : {}),
    ...(options.bot ? { bot: options.bot } : {}),
    ...(options.instance ? { instance: options.instance } : {}),
  });
  const explicit = options.bot && options.instance;
  const plugin = useMemo<PluginRef | undefined>(() => {
    if (explicit)
      return {
        bot: options.bot!,
        instance: options.instance!,
        ...(options.server ? { server: options.server } : {}),
      };
    if (!found.instance) return undefined;
    return {
      bot: found.instance.bot,
      instance: found.instance.instance,
      ...(options.server ? { server: options.server } : {}),
    };
  }, [explicit, options.bot, options.instance, options.server, found.instance]);

  const onCompleteRef = useRef(options.onComplete);
  onCompleteRef.current = options.onComplete;
  const query = usePluginQuery<OnboardingSteps>(plugin, "/steps");
  // On success the steps and the member are refetched (answers then equal the new roles).
  const mutation = usePluginMutation<OnboardingCompleteRequest, OnboardingCompleteResponse>(
    plugin,
    "/complete",
  );

  const steps = useMemo(() => query.data?.steps ?? [], [query.data]);
  const [index, setIndex] = useState(0);
  const [answers, setAnswers] = useState<OnboardingAnswers>({});
  const [result, setResult] = useState<OnboardingCompleteResponse | undefined>(undefined);
  const [serverErrors, setServerErrors] = useState<Record<string, string>>({});

  // Initialise answers from the member's current roles whenever the steps (re)load.
  useEffect(() => {
    if (!query.data) return;
    setAnswers(Object.fromEntries(query.data.steps.map((s) => [s.id, [...s.selected]])));
    setIndex((i) => Math.min(i, Math.max(0, query.data!.steps.length - 1)));
  }, [query.data]);

  const total = steps.length;
  const step = steps[index];
  const resolveStep = useCallback(
    (stepId?: string) => (stepId ? steps.find((s) => s.id === stepId) : step),
    [steps, step],
  );

  const selected = useCallback(
    (stepId?: string) => {
      const s = resolveStep(stepId);
      return s ? (answers[s.id] ?? []) : [];
    },
    [answers, resolveStep],
  );

  const setAnswer = useCallback(
    (stepId: string, roles: string[]) => {
      const s = steps.find((x) => x.id === stepId);
      if (!s) return;
      const allowed = optionRoles(s);
      const clean = [...new Set(roles)].filter((r) => allowed.has(r));
      setAnswers((a) => ({ ...a, [stepId]: s.multiple ? clean : clean.slice(-1) }));
      setServerErrors((e) => {
        if (!(stepId in e)) return e;
        const { [stepId]: _removed, ...rest } = e;
        return rest;
      });
    },
    [steps],
  );

  const toggle = useCallback(
    (role: string, stepId?: string) => {
      const s = resolveStep(stepId);
      if (!s || !optionRoles(s).has(role)) return;
      const current = answers[s.id] ?? [];
      const has = current.includes(role);
      if (s.multiple) setAnswer(s.id, has ? current.filter((r) => r !== role) : [...current, role]);
      else setAnswer(s.id, has ? [] : [role]);
    },
    [answers, resolveStep, setAnswer],
  );

  const stepValid = useCallback(
    (s: OnboardingStep) => !s.required || (answers[s.id]?.length ?? 0) > 0,
    [answers],
  );
  const canContinue = step ? stepValid(step) : false;

  const submit = useCallback(async () => {
    const firstInvalid = steps.findIndex((s) => !stepValid(s));
    if (firstInvalid >= 0) {
      setIndex(firstInvalid);
      return undefined;
    }
    const payload: OnboardingAnswers = Object.fromEntries(
      steps.map((s) => [s.id, answers[s.id] ?? []]),
    );
    try {
      const response = await mutation.mutateAsync({ answers: payload });
      setResult(response);
      setServerErrors({});
      onCompleteRef.current?.(response);
      return response;
    } catch (error) {
      if (error instanceof CodiApiError && error.diagnostics.length > 0) {
        const byStep: Record<string, string> = {};
        for (const d of error.diagnostics) {
          const id = /^\/answers\/([^/]+)/.exec(d.path ?? "")?.[1];
          if (id && !(id in byStep)) byStep[id] = d.message;
        }
        setServerErrors(byStep);
        const first = steps.findIndex((s) => s.id in byStep);
        if (first >= 0) setIndex(first);
      }
      return undefined;
    }
  }, [steps, answers, stepValid, mutation]);

  const next = useCallback(() => {
    if (!step || !canContinue) return;
    if (index >= total - 1) void submit();
    else setIndex(index + 1);
  }, [step, canContinue, index, total, submit]);

  const back = useCallback(() => setIndex((i) => Math.max(0, i - 1)), []);
  const goTo = useCallback((i: number) => setIndex(Math.max(0, Math.min(total - 1, i))), [total]);
  const restart = useCallback(() => {
    setResult(undefined);
    mutation.reset();
    setIndex(0);
  }, [mutation]);

  let status: OnboardingStatus;
  if (result) status = "complete";
  else if (mutation.isPending) status = "submitting";
  else if (!plugin) {
    status =
      found.status === "error" ? "error" : found.status === "success" ? "unavailable" : "loading";
  } else if (query.status === "error") {
    status =
      query.error instanceof CodiApiError && query.error.code === "unauthenticated"
        ? "unauthenticated"
        : "error";
  } else if (!query.data) status = "loading";
  else status = "ready";

  const error = status === "error" ? (found.error ?? query.error) : mutation.error;

  const stepErrors = useMemo(() => {
    const out = { ...serverErrors };
    if (mutation.error instanceof CodiApiError && mutation.error.diagnostics.length === 0 && step) {
      out[step.id] ??= mutation.error.message;
    }
    return out;
  }, [serverErrors, mutation.error, step]);

  const completedSteps = steps.filter((s, i) => i < index && stepValid(s)).length;

  return {
    status,
    error,
    data: query.data,
    plugin,
    steps,
    index,
    step,
    total,
    isFirst: index === 0,
    isLast: index === total - 1,
    progress: status === "complete" ? 1 : total === 0 ? 0 : completedSteps / total,
    answers,
    selected,
    isSelected: (role, stepId) => selected(stepId).includes(role),
    toggle,
    setAnswer,
    canContinue,
    stepErrors,
    next,
    back,
    goTo,
    submit,
    result,
    restart,
    reload: () => void query.refetch(),
  };
}

export interface OnboardingFlowProps extends UseOnboardingOptions {
  /** Render prop receiving the whole flow state. */
  children: (flow: OnboardingFlowState) => ReactNode;
}

/**
 * Headless onboarding flow for the `onboarding` plugin (`GET /steps`, `POST /complete`).
 *
 * ```tsx
 * <OnboardingFlow>
 *   {(flow) => flow.status === "ready" && (
 *     <section>
 *       <h2>{flow.step?.title}</h2>
 *       {flow.step?.options.map((o) => (
 *         <button key={o.role} aria-pressed={flow.isSelected(o.role)} onClick={() => flow.toggle(o.role)}>
 *           {o.emoji} {o.label}
 *         </button>
 *       ))}
 *       <button disabled={!flow.canContinue} onClick={flow.next}>{flow.isLast ? "Finish" : "Next"}</button>
 *     </section>
 *   )}
 * </OnboardingFlow>
 * ```
 */
export function OnboardingFlow({ children, ...options }: OnboardingFlowProps) {
  const flow = useOnboarding(options);
  return <>{children(flow)}</>;
}
