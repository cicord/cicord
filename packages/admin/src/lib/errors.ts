/**
 * Error presentation rules:
 *  - the main text is always a sentence for people (never raw JSON or HTTP noise);
 *  - the hint says what to do;
 *  - the raw technical text is kept in `details`, shown collapsed with a copy button.
 * The runtime explains Discord / reconcile errors itself (`error` objects, https://cicord.dev/docs/reference/http-api/);
 * `explainRaw` is the fallback for older runtimes and free-form messages.
 */
import { CicordApiError, type HumanError } from "@cicord/client";
import { i18n } from "../i18n/index.ts";

/** Strips a trailing JSON blob, "HTTP 400 50035:" noise and Go error prefixes. */
export function cleanMessage(raw: string): string {
  let s = raw.split("\n")[0]!.trim();
  // Drop JSON objects ({…} balanced) anywhere in the text.
  for (let guard = 0; guard < 5; guard++) {
    const start = s.indexOf("{");
    if (start < 0) break;
    let depth = 0;
    let end = -1;
    for (let i = start; i < s.length; i++) {
      if (s[i] === "{") depth++;
      else if (s[i] === "}" && --depth === 0) {
        end = i;
        break;
      }
    }
    if (end < 0) break;
    s = `${s.slice(0, start)}${s.slice(end + 1)}`.replace(/\s+/g, " ").trim();
  }
  s = s
    .replace(/^reconcile \S+: \d+ change\(s\) failed: /, "")
    .replace(/\bdiscord \w+: /g, "")
    .replace(/discord: (bad request|missing permissions|not found|invalid token)/, "")
    .replace(/HTTP \d{3} \d+: /, "")
    .replace(/\(\s+/g, "(")
    .replace(/\s+\)/g, ")")
    .replace(/\(\s*\)/g, "")
    .replace(/:\s*\(([^()]+)\)/g, ": $1")
    .replace(/[:\s(]+$/, "")
    .replace(/\s+/g, " ")
    .replace(/ :/g, ":")
    .trim();
  return s ? s[0]!.toUpperCase() + s.slice(1) : raw.trim();
}

/** Client-side fallback when the runtime sent no `error` object. */
export function explainRaw(raw: string): HumanError {
  return { code: "unknown", message: cleanMessage(raw), details: raw };
}

/** The `error` object of a status / history entry, or one derived from its raw message. */
export function errorOf(entry: {
  error?: HumanError | undefined;
  message?: string;
}): HumanError | null {
  if (entry.error) return entry.error;
  return entry.message ? explainRaw(entry.message) : null;
}

type ErrorKind =
  | "network"
  | "unauthenticated"
  | "forbidden"
  | "not_found"
  | "conflict"
  | "invalid"
  | "unavailable"
  | "internal"
  | "other";

export interface DescribedError {
  kind: ErrorKind;
  /** Short title ("You don't have access"). */
  title: string;
  /** One sentence for people. */
  message: string;
  hint?: string;
  /** Raw technical text, for the collapsed details block. */
  details?: string;
}

/** Maps any thrown value (CicordApiError or not) to text for a toast or an inline message. */
export function describeError(error: unknown): DescribedError {
  const t = i18n.t.bind(i18n);
  if (!(error instanceof CicordApiError)) {
    const raw = error instanceof Error ? error.message : String(error);
    return {
      kind: "other",
      title: t("errors.generic.title"),
      message: cleanMessage(raw),
      details: raw,
    };
  }
  const raw = error.details ?? error.message;
  const technical = raw !== error.message || /[{}]|HTTP \d{3}|discord: /.test(error.message);
  const human = technical && !error.details ? cleanMessage(error.message) : error.message;
  const details = technical ? raw : undefined;
  const withHint = (d: DescribedError): DescribedError =>
    error.hint ? { ...d, hint: error.hint } : d;
  const opt = (d: DescribedError): DescribedError =>
    details && details !== d.message ? { ...d, details } : d;
  switch (error.code) {
    case "network":
      return {
        kind: "network",
        title: t("errors.network.title"),
        message: t("errors.network.message"),
        hint: t("errors.network.hint"),
      };
    case "unauthenticated":
      return {
        kind: "unauthenticated",
        title: t("errors.unauthenticated.title"),
        message: t("errors.unauthenticated.message"),
      };
    case "forbidden":
      return opt({
        kind: "forbidden",
        title: t("errors.forbidden.title"),
        message: human || t("errors.forbidden.message"),
        hint: t("errors.forbidden.hint"),
      });
    case "not_found":
      return opt({
        kind: "not_found",
        title: t("errors.notFound.title"),
        message: human || t("errors.notFound.message"),
      });
    case "conflict":
      return opt({
        kind: "conflict",
        title: t("errors.conflict.title"),
        message: human || t("errors.conflict.message"),
        hint: t("errors.conflict.hint"),
      });
    case "invalid": {
      const n = error.diagnostics.length;
      return opt({
        kind: "invalid",
        title: n ? t("errors.invalid.titleCount", { count: n }) : t("errors.invalid.title"),
        message: human,
        hint: t("errors.invalid.hint"),
      });
    }
    case "unavailable":
      return opt(
        withHint({
          kind: "unavailable",
          title: error.reason ? t("errors.discord.title") : t("errors.unavailable.title"),
          message: human || t("errors.unavailable.message"),
          ...(error.hint ? {} : { hint: t("errors.unavailable.hint") }),
        }),
      );
    case "internal":
      return {
        kind: "internal",
        title: t("errors.internal.title"),
        message: t("errors.internal.message"),
        hint: t("errors.internal.hint"),
        ...(error.message ? { details: error.message } : {}),
      };
    default:
      return opt(withHint({ kind: "other", title: t("errors.generic.title"), message: human }));
  }
}
