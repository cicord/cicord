/** Routes and the navigation model shared by the sidebar, breadcrumb and ⌘K palette. */
import {
  ChartColumn,
  FlaskConical,
  GitCompareArrows,
  History,
  LayoutDashboard,
  ListChecks,
  Stethoscope,
  Puzzle,
  ScrollText,
  Settings,
  Shapes,
  SlidersHorizontal,
  Store,
  type LucideIcon,
} from "lucide-react";
import { match } from "../router.tsx";

export type PageId =
  | "overview"
  | "analytics"
  | "resources"
  | "serverSettings"
  | "plugins"
  | "store"
  | "plan"
  | "history"
  | "drift"
  | "audit"
  | "simulator"
  | "settings"
  | "diagnostics"
  | "home"
  | "notFound";

export type SectionId = "configuration" | "sync";

export interface NavItem {
  id: PageId;
  icon: LucideIcon;
  /** Path segment under /servers/:key ("" = overview); absent for global pages. */
  segment?: string;
  /** Global path (simulator, settings). */
  path?: string;
  section?: SectionId;
  adminOnly?: boolean;
  devOnly?: boolean;
}

export const NAV: NavItem[] = [
  { id: "overview", icon: LayoutDashboard, segment: "" },
  { id: "analytics", icon: ChartColumn, segment: "analytics" },
  { id: "resources", icon: Shapes, segment: "resources", section: "configuration" },
  { id: "serverSettings", icon: SlidersHorizontal, segment: "settings", section: "configuration" },
  { id: "plugins", icon: Puzzle, segment: "plugins", section: "configuration" },
  { id: "store", icon: Store, segment: "store", section: "configuration", adminOnly: true },
  { id: "plan", icon: ListChecks, segment: "plan", section: "sync", adminOnly: true },
  { id: "history", icon: History, segment: "history", section: "sync" },
  { id: "drift", icon: GitCompareArrows, segment: "drift", section: "sync" },
  { id: "audit", icon: ScrollText, segment: "audit" },
  { id: "diagnostics", icon: Stethoscope, segment: "diagnostics", adminOnly: true },
  { id: "simulator", icon: FlaskConical, path: "/simulator", devOnly: true },
  { id: "settings", icon: Settings, path: "/settings" },
];

export function navItem(id: PageId): NavItem | undefined {
  return NAV.find((n) => n.id === id);
}

export function pathFor(item: NavItem, serverKey: string | undefined): string {
  if (item.path) return item.path;
  if (!serverKey) return "/";
  return item.segment
    ? `/servers/${encodeURIComponent(serverKey)}/${item.segment}`
    : `/servers/${encodeURIComponent(serverKey)}`;
}

export interface Route {
  page: PageId;
  serverKey?: string;
  /** A plugin instance's page (/servers/:key/plugins/:bot/:instance). */
  instance?: { bot: string; instance: string };
  /** A Store plugin's page (/servers/:key/store/:source/:plugin). */
  storePlugin?: { source: string; plugin: string };
  /** The review of every available update (/servers/:key/store/updates). */
  storeUpdates?: true;
}

/** Path of a plugin instance's page. */
export const instancePath = (serverKey: string, bot: string, instance: string): string =>
  `/servers/${encodeURIComponent(serverKey)}/plugins/${encodeURIComponent(bot)}/${encodeURIComponent(instance)}`;

/** Path of a Store plugin's page. */
export const storePluginPath = (serverKey: string, source: string, plugin: string): string =>
  `/servers/${encodeURIComponent(serverKey)}/store/${encodeURIComponent(source)}/${encodeURIComponent(plugin)}`;

/** Path of the review of every available update (one change). */
export const storeUpdatesPath = (serverKey: string): string =>
  `/servers/${encodeURIComponent(serverKey)}/store/updates`;

/** Path of the update review of an installed Store plugin. */
export const storeUpdatePath = (serverKey: string, source: string, plugin: string): string =>
  `${storePluginPath(serverKey, source, plugin)}?update=1`;

const SEGMENTS: Record<string, PageId> = Object.fromEntries(
  NAV.filter((n) => n.segment).map((n) => [n.segment!, n.id]),
);

export function parseRoute(path: string): Route {
  if (path === "/" || path === "/login" || path === "") return { page: "home" };
  if (path === "/simulator") return { page: "simulator" };
  if (path === "/settings") return { page: "settings" };
  let p: Record<string, string> | null;
  if ((p = match("/servers/:key", path))) return { page: "overview", serverKey: p["key"]! };
  if ((p = match("/servers/:key/:segment", path))) {
    const seg = p["segment"]!;
    if (seg === "status") return { page: "overview", serverKey: p["key"]! };
    const page = SEGMENTS[seg];
    if (page) return { page, serverKey: p["key"]! };
  }
  if ((p = match("/servers/:key/plugins/:bot/:instance", path)))
    return {
      page: "plugins",
      serverKey: p["key"]!,
      instance: { bot: p["bot"]!, instance: p["instance"]! },
    };
  if ((p = match("/servers/:key/store/updates", path)))
    return { page: "store", serverKey: p["key"]!, storeUpdates: true };
  if ((p = match("/servers/:key/store/:source/:plugin", path)))
    return {
      page: "store",
      serverKey: p["key"]!,
      storePlugin: { source: p["source"]!, plugin: p["plugin"]! },
    };
  return { page: "notFound" };
}
