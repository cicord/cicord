import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import { legibleOn, parseHex, readableOn, toHex, type Rgb } from "./color.ts";

/** Server theme (`spec.portal.theme`, as served by `GET /api/v1/portal/{server}`). */
export interface CicordTheme {
  primary?: string | undefined;
  accent?: string | undefined;
  logo?: string | undefined;
  /** "" (sent by the runtime when unset) behaves like undefined: the default mode. */
  mode?: "light" | "dark" | "system" | "" | undefined;
}

export type ResolvedMode = "light" | "dark";

export const DEFAULT_PRIMARY = "#5865f2";
export const DEFAULT_ACCENT = "#eb459e";

// Surfaces the derived colors are checked against (keep in sync with styles.css).
const SURFACE: Record<ResolvedMode, Rgb> = { dark: [27, 29, 38], light: [255, 255, 255] };

/** CSS custom properties for a theme in a given mode. */
export function themeVariables(
  theme: CicordTheme | undefined,
  mode: ResolvedMode,
): Record<string, string> {
  const primary = parseHex(theme?.primary) ?? (parseHex(DEFAULT_PRIMARY) as Rgb);
  const accent = parseHex(theme?.accent) ?? (parseHex(DEFAULT_ACCENT) as Rgb);
  const surface = SURFACE[mode];
  return {
    "--cicord-primary": toHex(primary),
    "--cicord-primary-fg": readableOn(primary),
    "--cicord-primary-text": legibleOn(primary, surface),
    "--cicord-accent": toHex(accent),
    "--cicord-accent-fg": readableOn(accent),
    "--cicord-accent-text": legibleOn(accent, surface),
  };
}

function systemMode(): ResolvedMode {
  if (typeof window === "undefined" || !window.matchMedia) return "dark";
  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

/** Resolves "system" with `prefers-color-scheme` and follows its changes. Default: dark. */
export function useResolvedMode(mode: CicordTheme["mode"]): ResolvedMode {
  const [system, setSystem] = useState<ResolvedMode>(systemMode);
  useEffect(() => {
    if (mode !== "system" || typeof window === "undefined" || !window.matchMedia) return;
    const media = window.matchMedia("(prefers-color-scheme: light)");
    const update = () => setSystem(media.matches ? "light" : "dark");
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, [mode]);
  if (mode === "light" || mode === "dark") return mode;
  if (mode === "system") return system;
  return "dark";
}

interface ThemeContextValue {
  theme: CicordTheme;
  mode: ResolvedMode;
}

const ThemeContext = createContext<ThemeContextValue>({ theme: {}, mode: "dark" });

export function useCicordTheme(): ThemeContextValue {
  return useContext(ThemeContext);
}

export interface ThemeProviderProps {
  theme?: CicordTheme | undefined;
  /** Overrides `theme.mode`. */
  mode?: CicordTheme["mode"];
  /**
   * Apply the variables and mode to `<html>` too, so the page background, scrollbars and
   * form controls follow the theme. Use it once, at the root of a portal.
   */
  global?: boolean;
  className?: string;
  style?: CSSProperties;
  children?: ReactNode;
}

/**
 * Maps the server theme `{primary, accent, mode}` to CSS variables (`--cicord-*`) and a
 * `data-cicord-mode` attribute that switches the light/dark palettes of styles.css.
 */
export function ThemeProvider({
  theme,
  mode,
  global = false,
  className,
  style,
  children,
}: ThemeProviderProps) {
  const resolved = useResolvedMode(mode ?? theme?.mode);
  const vars = useMemo(() => themeVariables(theme, resolved), [theme, resolved]);
  const value = useMemo(() => ({ theme: theme ?? {}, mode: resolved }), [theme, resolved]);

  useEffect(() => {
    if (!global || typeof document === "undefined") return;
    const root = document.documentElement;
    const previous = root.getAttribute("data-cicord-mode");
    root.setAttribute("data-cicord-mode", resolved);
    root.style.colorScheme = resolved;
    for (const [k, v] of Object.entries(vars)) root.style.setProperty(k, v);
    root.classList.add("cicord-root");
    return () => {
      if (previous === null) root.removeAttribute("data-cicord-mode");
      else root.setAttribute("data-cicord-mode", previous);
      for (const k of Object.keys(vars)) root.style.removeProperty(k);
    };
  }, [global, resolved, vars]);

  return (
    <ThemeContext.Provider value={value}>
      <div
        className={["cicord-root", className].filter(Boolean).join(" ")}
        data-cicord-mode={resolved}
        style={{ ...(vars as CSSProperties), colorScheme: resolved, ...style }}
      >
        {children}
      </div>
    </ThemeContext.Provider>
  );
}
