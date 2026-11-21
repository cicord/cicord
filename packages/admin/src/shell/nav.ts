/** Routes and the navigation model shared by the sidebar, breadcrumb and ⌘K palette. */
import {
  FlaskConical,
  GitCompareArrows,
  History,
  LayoutDashboard,
  ListChecks,
  Puzzle,
  ScrollText,
  Settings,
  Shapes,
  SlidersHorizontal,
  type LucideIcon,
} from "lucide-react";
import { match } from "../router.tsx";

export type PageId =
  | "overview"
  | "resources"
  | "serverSettings"
  | "plugins"
  | "plan"
  | "history"
  | "drift"
  | "audit"
  | "simulator"
  | "settings"
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
  { id: "resources", icon: Shapes, segment: "resources", section: "configuration" },
  { id: "serverSettings", icon: SlidersHorizontal, segment: "settings", section: "configuration" },
  { id: "plugins", icon: Puzzle, segment: "plugins", section: "configuration" },
  { id: "plan", icon: ListChecks, segment: "plan", section: "sync", adminOnly: true },
  { id: "history", icon: History, segment: "history", section: "sync" },
  { id: "drift", icon: GitCompareArrows, segment: "drift", section: "sync" },
  { id: "audit", icon: ScrollText, segment: "audit" },
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
}

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
  return { page: "notFound" };
}
