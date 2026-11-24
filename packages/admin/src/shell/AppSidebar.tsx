import type { Me } from "@cicord/client";
import { useLogout } from "@cicord/react";
import { Check, ChevronsUpDown, LogOut, Monitor, Moon, Sun } from "lucide-react";
import { ServerAvatar, UserAvatar } from "../components/common.tsx";
import { StatusDot, healthTone, syncTone } from "../components/status.tsx";
import { Skeleton } from "../components/ui/skeleton.tsx";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../components/ui/dropdown-menu.tsx";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "../components/ui/sidebar.tsx";
import { BrandMark, RoleBadge, useViewer, useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { Link, navigate } from "../router.tsx";
import { NAV, pathFor, type NavItem, type PageId, type SectionId } from "./nav.ts";
import { useTheme, type ThemePreference } from "./theme.tsx";

export function AppSidebar({ page }: { page: PageId }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const drift = ws.server?.drift.length ?? 0;
  const visible = NAV.filter((n) => (!n.devOnly || ws.simulator) && (!n.adminOnly || ws.isAdmin));
  // Overview · Configuration · Sync · Audit log … Simulator, Settings (bottom).
  const top = visible.filter((n) => n.id === "overview");
  const audit = visible.filter((n) => n.id === "audit" || n.id === "diagnostics");
  const section = (id: SectionId) => visible.filter((n) => n.section === id);
  const tail = visible.filter((n) => n.id === "simulator" || n.id === "settings");

  return (
    <Sidebar label={t("nav.label")}>
      <SidebarHeader className="gap-1 pb-1">
        <Link
          to={ws.serverKey ? `/servers/${ws.serverKey}` : "/"}
          className="flex h-9 items-center gap-2 rounded-md px-2 text-foreground outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0"
          aria-label={t("nav.home")}
        >
          <BrandMark size={20} />
          <span className="text-[15px] font-semibold tracking-tight group-data-[collapsible=icon]:hidden">
            cicord
          </span>
          <span className="text-[13px] text-subtle-foreground group-data-[collapsible=icon]:hidden">
            {t("app.admin")}
          </span>
        </Link>
        <ServerSwitcher />
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarMenu>
            {top.map((n) => (
              <NavEntry key={n.id} item={n} active={page === n.id} />
            ))}
          </SidebarMenu>
        </SidebarGroup>
        {(["configuration", "sync"] as const).map((s) =>
          section(s).length ? (
            <SidebarGroup key={s}>
              <SidebarGroupLabel>{t(`nav.sections.${s}`)}</SidebarGroupLabel>
              <SidebarMenu>
                {section(s).map((n) => (
                  <NavEntry
                    key={n.id}
                    item={n}
                    active={page === n.id}
                    badge={n.id === "drift" && drift > 0 ? drift : undefined}
                  />
                ))}
              </SidebarMenu>
            </SidebarGroup>
          ) : null,
        )}
        <SidebarGroup>
          <SidebarMenu>
            {audit.map((n) => (
              <NavEntry key={n.id} item={n} active={page === n.id} />
            ))}
          </SidebarMenu>
        </SidebarGroup>
        <SidebarGroup className="mt-auto">
          <SidebarMenu>
            {tail.map((n) => (
              <NavEntry key={n.id} item={n} active={page === n.id} />
            ))}
          </SidebarMenu>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <UserMenu me={useViewer()} />
      </SidebarFooter>
      <SidebarRail label={t("nav.toggleSidebar")} />
    </Sidebar>
  );
}

function NavEntry({
  item,
  active,
  badge,
}: {
  item: NavItem;
  active: boolean;
  badge?: number | undefined;
}) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { isMobile, setOpenMobile } = useSidebar();
  const label = t(`nav.pages.${item.id as Exclude<PageId, "home" | "notFound">}`);
  const Icon = item.icon;
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={active} tooltip={label}>
        <Link
          to={pathFor(item, ws.serverKey)}
          aria-current={active ? "page" : undefined}
          onClick={() => isMobile && setOpenMobile(false)}
        >
          <Icon aria-hidden="true" />
          <span>{label}</span>
          {item.devOnly ? (
            <span className="ml-auto rounded border border-border-strong px-1 text-[10px] font-medium text-subtle-foreground group-data-[collapsible=icon]:hidden">
              {t("nav.dev")}
            </span>
          ) : null}
        </Link>
      </SidebarMenuButton>
      {badge ? (
        <SidebarMenuBadge
          className="bg-warning-soft text-warning"
          aria-label={t("nav.driftCount", { count: badge })}
        >
          {badge}
        </SidebarMenuBadge>
      ) : null}
    </SidebarMenuItem>
  );
}

/** Workspace-style server switcher: server icon + name + sync dot. While the status
 * loads it shows placeholders (and the server name when the session knows it), never the
 * raw key; "Pick a server" only when there is no current server. */
export function ServerSwitcher() {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const servers = ws.status?.servers ?? [];
  const current = ws.server;
  const loading = !current && ws.loading && ws.serverKey !== undefined;
  const name = ws.serverName ?? (loading ? undefined : (ws.serverKey ?? t("switcher.none")));
  const label = name ?? t("switcher.loading");
  const tone = current
    ? current.health === "Degraded" || current.health === "Missing"
      ? healthTone(current.health)
      : syncTone(current.sync)
    : "neutral";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <SidebarMenuButton
          className="h-11 border border-sidebar-border bg-background/40 px-2 data-[state=open]:bg-sidebar-accent group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:border-0 group-data-[collapsible=icon]:bg-transparent"
          aria-label={t("switcher.label", { name: label })}
          aria-busy={loading || undefined}
          tooltip={label}
        >
          {loading ? (
            <Skeleton className="size-[26px] shrink-0 rounded-md" />
          ) : (
            <span className="relative">
              <ServerAvatar name={label} url={current?.iconUrl} size={26} />
              <span className="absolute -right-0.5 -bottom-0.5 rounded-full bg-sidebar p-[2px]">
                <StatusDot tone={tone} className="block size-2" />
              </span>
            </span>
          )}
          <span className="flex min-w-0 flex-1 flex-col gap-0.5 leading-tight group-data-[collapsible=icon]:hidden">
            {name !== undefined ? (
              <span className="truncate text-[13px] font-medium text-foreground">{name}</span>
            ) : (
              <Skeleton className="h-3.5 w-24" />
            )}
            {current ? (
              <span className="truncate text-xs text-subtle-foreground">
                {t(`status.sync.${current.sync}.label`, { defaultValue: current.sync })}
              </span>
            ) : loading ? (
              <Skeleton className="h-3 w-14" />
            ) : (
              <span className="truncate text-xs text-subtle-foreground">{t("switcher.pick")}</span>
            )}
          </span>
          <ChevronsUpDown
            className="ml-auto size-3.5 group-data-[collapsible=icon]:hidden"
            aria-hidden="true"
          />
        </SidebarMenuButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64" aria-label={t("switcher.servers")}>
        <DropdownMenuLabel>{t("switcher.servers")}</DropdownMenuLabel>
        {servers.map((s) => (
          <DropdownMenuItem key={s.key} onSelect={() => ws.selectServer(s.key)} className="gap-2.5">
            <ServerAvatar name={s.name} url={s.iconUrl} size={22} />
            <span className="flex min-w-0 flex-1 flex-col leading-tight">
              <span className="truncate">{s.name}</span>
              <span className="truncate font-mono text-[11px] text-subtle-foreground">{s.key}</span>
            </span>
            <StatusDot tone={s.health === "Degraded" ? "danger" : syncTone(s.sync)} />
            {s.key === ws.serverKey ? (
              <Check className="size-3.5 text-foreground" aria-label={t("switcher.current")} />
            ) : null}
          </DropdownMenuItem>
        ))}
        {servers.length === 0 ? (
          <p className="px-2 py-1.5 text-[13px] text-muted-foreground">
            {ws.loading ? t("switcher.loading") : t("switcher.empty")}
          </p>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function UserMenu({ me }: { me: Me }) {
  const { t } = useTranslation();
  const logout = useLogout();
  const { preference, setPreference } = useTheme();
  const ws = useWorkspace();
  const name = me.user.displayName || me.user.username;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <SidebarMenuButton
          className="h-10 data-[state=open]:bg-sidebar-accent"
          aria-label={t("user.menu", { name })}
          tooltip={name}
        >
          <UserAvatar name={name} url={me.user.avatarUrl} size={24} />
          <span className="flex min-w-0 flex-1 flex-col leading-tight group-data-[collapsible=icon]:hidden">
            <span className="truncate text-[13px] font-medium text-foreground">{name}</span>
            <span className="truncate text-xs text-subtle-foreground">
              {ws.role ? t(`roles.${ws.role}`) : `@${me.user.username}`}
            </span>
          </span>
          <ChevronsUpDown
            className="ml-auto size-3.5 group-data-[collapsible=icon]:hidden"
            aria-hidden="true"
          />
        </SidebarMenuButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-64">
        <div className="flex items-center gap-2.5 px-2 py-2">
          <UserAvatar name={name} url={me.user.avatarUrl} size={32} />
          <div className="min-w-0">
            <p className="truncate text-[13px] font-medium">{name}</p>
            <p className="truncate text-xs text-muted-foreground">
              @{me.user.username} · <span className="font-mono tabular">{me.user.id}</span>
            </p>
          </div>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>{t("user.access")}</DropdownMenuLabel>
        {me.servers.map((s) => (
          <div
            key={s.key}
            className="flex items-center justify-between gap-2 px-2 py-1 text-[13px]"
          >
            <span className="truncate">{s.name}</span>
            <RoleBadge role={s.role} />
          </div>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuLabel>{t("settings.theme.label")}</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={preference}
          onValueChange={(v) => setPreference(v as ThemePreference)}
        >
          <DropdownMenuRadioItem value="system">
            <Monitor />
            {t("settings.theme.system")}
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="light">
            <Sun />
            {t("settings.theme.light")}
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">
            <Moon />
            {t("settings.theme.dark")}
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() => void logout().then(() => navigate("/", { replace: true }))}
        >
          <LogOut />
          {t("user.logout")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
