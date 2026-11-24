/** ⌘K / Ctrl+K: jump to a page, a server or a plugin instance; theme; log out. */
import { useLogout } from "@cicord/react";
import { LogOut, Moon, Search, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { usePlugins } from "../api.ts";
import { PluginGlyph, ServerAvatar } from "../components/common.tsx";
import { StatusDot, syncTone } from "../components/status.tsx";
import { Button } from "../components/ui/button.tsx";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "../components/ui/command.tsx";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "../components/ui/dialog.tsx";
import { Kbd } from "../components/ui/kbd.tsx";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { navigate } from "../router.tsx";
import { instancePath, NAV, pathFor, type PageId } from "./nav.ts";
import { useTheme } from "./theme.tsx";

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

export function CommandPaletteButton({ onOpen }: { onOpen: () => void }) {
  const { t } = useTranslation();
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={onOpen}
      className="h-8 w-8 justify-center gap-2 px-0 text-muted-foreground sm:w-56 sm:justify-start sm:px-2.5"
      aria-label={t("palette.open")}
    >
      <Search />
      <span className="hidden flex-1 text-left font-normal sm:inline">
        {t("palette.placeholderShort")}
      </span>
      <Kbd className="hidden sm:inline-flex">{isMac ? "⌘K" : "Ctrl K"}</Kbd>
    </Button>
  );
}

export function useCommandPalette(): [boolean, (open: boolean) => void] {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return [open, setOpen];
}

export function CommandPalette({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { theme, toggle } = useTheme();
  const logout = useLogout();
  const plugins = usePlugins(open ? ws.serverKey : undefined);
  const go = (to: string) => {
    onOpenChange(false);
    navigate(to);
  };
  const pages = NAV.filter((n) => (!n.devOnly || ws.simulator) && (!n.adminOnly || ws.isAdmin));
  const pageLabel = (id: PageId) => t(`nav.pages.${id as Exclude<PageId, "home" | "notFound">}`);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[18%] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-xl">
        <DialogTitle className="sr-only">{t("palette.title")}</DialogTitle>
        <DialogDescription className="sr-only">{t("palette.description")}</DialogDescription>
        <Command loop>
          <CommandInput placeholder={t("palette.placeholder")} />
          <CommandList>
            <CommandEmpty>{t("palette.empty")}</CommandEmpty>
            <CommandGroup
              heading={
                ws.server ? t("palette.pagesOf", { name: ws.server.name }) : t("palette.pages")
              }
            >
              {pages.map((n) => {
                const Icon = n.icon;
                const section = n.section ? t(`nav.sections.${n.section}`) : "";
                return (
                  <CommandItem
                    key={n.id}
                    value={`page ${pageLabel(n.id)} ${section}`}
                    onSelect={() => go(pathFor(n, ws.serverKey))}
                  >
                    <Icon />
                    <span>{pageLabel(n.id)}</span>
                    {section ? (
                      <span className="ml-auto text-xs text-subtle-foreground">{section}</span>
                    ) : null}
                  </CommandItem>
                );
              })}
            </CommandGroup>
            {ws.status?.servers.length ? (
              <CommandGroup heading={t("palette.servers")}>
                {ws.status.servers.map((s) => (
                  <CommandItem
                    key={s.key}
                    value={`server ${s.name} ${s.key}`}
                    onSelect={() => {
                      onOpenChange(false);
                      ws.selectServer(s.key);
                    }}
                  >
                    <ServerAvatar name={s.name} url={s.iconUrl} size={18} />
                    <span>{s.name}</span>
                    <span className="font-mono text-[11px] text-subtle-foreground">{s.key}</span>
                    <StatusDot tone={syncTone(s.sync)} className="ml-auto" />
                  </CommandItem>
                ))}
              </CommandGroup>
            ) : null}
            {plugins.data?.length ? (
              <CommandGroup heading={t("palette.plugins")}>
                {plugins.data.map((p) => (
                  <CommandItem
                    key={`${p.bot}/${p.instance}`}
                    value={`plugin ${p.plugin.displayName || p.plugin.name} ${p.instance} ${p.bot}`}
                    onSelect={() => go(instancePath(ws.serverKey!, p.bot, p.instance))}
                  >
                    <PluginGlyph size={18} />
                    <span>{p.plugin.displayName || p.plugin.name}</span>
                    <span className="font-mono text-[11px] text-subtle-foreground">
                      {p.bot}/{p.instance}
                    </span>
                  </CommandItem>
                ))}
              </CommandGroup>
            ) : null}
            <CommandSeparator />
            <CommandGroup heading={t("palette.actions")}>
              <CommandItem
                value="action theme toggle dark light"
                onSelect={() => {
                  toggle();
                  onOpenChange(false);
                }}
              >
                {theme === "dark" ? <Sun /> : <Moon />}
                {theme === "dark" ? t("palette.lightTheme") : t("palette.darkTheme")}
              </CommandItem>
              <CommandItem
                value="action log out sign out"
                onSelect={() => {
                  onOpenChange(false);
                  void logout().then(() => navigate("/", { replace: true }));
                }}
              >
                <LogOut />
                {t("user.logout")}
              </CommandItem>
            </CommandGroup>
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
