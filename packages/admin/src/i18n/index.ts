/**
 * i18n: every UI string lives in ./en.json (typed keys, i18next plurals `_one`/`_other`,
 * `{{interpolation}}`). Add a language by adding `<lng>.json` with the same keys to
 * `resources` below. Dates and relative times go through Intl in the current language.
 * Initialized synchronously on import so tests and the app render translated text at once.
 */
import i18next from "i18next";
import { initReactI18next, useTranslation } from "react-i18next";
import en from "./en.json";

export const resources = { en: { translation: en } } as const;
export type Language = keyof typeof resources;
export const LANGUAGES: { id: Language; name: string }[] = [{ id: "en", name: "English" }];

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "translation";
    resources: (typeof resources)["en"];
  }
}

const LANG_KEY = "codi-admin:lang";

function initialLanguage(): Language {
  try {
    const saved = localStorage.getItem(LANG_KEY);
    if (saved && saved in resources) return saved as Language;
  } catch {
    // storage unavailable
  }
  return "en";
}

void i18next.use(initReactI18next).init({
  resources,
  lng: initialLanguage(),
  fallbackLng: "en",
  initAsync: false,
  interpolation: { escapeValue: false },
  returnNull: false,
});

export function setLanguage(lng: Language): void {
  void i18next.changeLanguage(lng);
  try {
    localStorage.setItem(LANG_KEY, lng);
  } catch {
    // ignore
  }
  document.documentElement.lang = lng;
}

export { i18next as i18n, useTranslation };

// ───────────────────────────────────────────────────────────── formatting

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 31536000],
  ["month", 2592000],
  ["week", 604800],
  ["day", 86400],
  ["hour", 3600],
  ["minute", 60],
  ["second", 1],
];

/** "3 minutes ago", "in 2 hours"; "just now" under 10 s. */
export function formatRelative(
  iso: string,
  now: number = Date.now(),
  lng = i18next.language,
): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  const diff = Math.round((t - now) / 1000);
  // Under 10 s, or slightly in the future (server clock ahead of the browser).
  if (Math.abs(diff) < 10 || (diff > 0 && diff < 120)) return i18next.t("time.justNow");
  const rtf = new Intl.RelativeTimeFormat(lng, { numeric: "auto" });
  for (const [unit, secs] of UNITS) {
    if (Math.abs(diff) >= secs || unit === "second")
      return rtf.format(Math.round(diff / secs), unit);
  }
  return "—";
}

/** Absolute date + time, e.g. "Nov 21, 2026, 10:04:12". */
export function formatDateTime(iso: string, lng = i18next.language): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return new Intl.DateTimeFormat(lng, { dateStyle: "medium", timeStyle: "medium" }).format(d);
}

export function formatNumber(n: number, lng = i18next.language): string {
  return new Intl.NumberFormat(lng).format(n);
}

/** True for Go's zero time and empty values. */
export function isNever(iso: string | undefined | null): boolean {
  return !iso || iso.startsWith("0001-");
}
