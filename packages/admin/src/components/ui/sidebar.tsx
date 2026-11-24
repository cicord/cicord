/**
 * Collapsible application sidebar (after shadcn/ui's Sidebar, trimmed to what the console
 * uses): expanded / icon-only on desktop (state persisted), an off-canvas Sheet below the
 * `md` breakpoint. ⌘B / Ctrl+B toggles it.
 */
import { PanelLeft } from "lucide-react";
import { Slot } from "radix-ui";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ComponentProps,
  type CSSProperties,
} from "react";
import { cn } from "../../lib/utils.ts";
import { Button } from "./button.tsx";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "./sheet.tsx";
import { Tooltip, TooltipContent, TooltipTrigger } from "./tooltip.tsx";

const STORAGE_KEY = "cicord-admin:sidebar";
const WIDTH = "15rem";
const WIDTH_ICON = "3.25rem";
const MOBILE_QUERY = "(max-width: 767px)";

function useIsMobile(): boolean {
  const [mobile, setMobile] = useState(() =>
    typeof window === "undefined" ? false : window.matchMedia(MOBILE_QUERY).matches,
  );
  useEffect(() => {
    const mq = window.matchMedia(MOBILE_QUERY);
    const on = () => setMobile(mq.matches);
    mq.addEventListener("change", on);
    on();
    return () => mq.removeEventListener("change", on);
  }, []);
  return mobile;
}

interface SidebarState {
  state: "expanded" | "collapsed";
  open: boolean;
  setOpen: (open: boolean) => void;
  openMobile: boolean;
  setOpenMobile: (open: boolean) => void;
  isMobile: boolean;
  toggle: () => void;
}

const SidebarContext = createContext<SidebarState | null>(null);

export function useSidebar(): SidebarState {
  const ctx = useContext(SidebarContext);
  if (!ctx) throw new Error("useSidebar must be used within <SidebarProvider>");
  return ctx;
}

function savedOpen(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) !== "collapsed";
  } catch {
    return true;
  }
}

export function SidebarProvider({ className, style, children, ...props }: ComponentProps<"div">) {
  const isMobile = useIsMobile();
  const [openMobile, setOpenMobile] = useState(false);
  const [open, setOpenState] = useState(savedOpen);
  const setOpen = useCallback((value: boolean) => {
    setOpenState(value);
    try {
      localStorage.setItem(STORAGE_KEY, value ? "expanded" : "collapsed");
    } catch {
      // storage unavailable
    }
  }, []);
  const toggle = useCallback(
    () => (isMobile ? setOpenMobile((o) => !o) : setOpen(!open)),
    [isMobile, open, setOpen],
  );

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "b" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        toggle();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [toggle]);

  const value = useMemo<SidebarState>(
    () => ({
      state: open ? "expanded" : "collapsed",
      open,
      setOpen,
      openMobile,
      setOpenMobile,
      isMobile,
      toggle,
    }),
    [open, setOpen, openMobile, isMobile, toggle],
  );

  return (
    <SidebarContext.Provider value={value}>
      <div
        style={
          {
            "--sidebar-width": WIDTH,
            "--sidebar-width-icon": WIDTH_ICON,
            ...style,
          } as CSSProperties
        }
        className={cn("group/sidebar-wrapper flex min-h-svh w-full", className)}
        {...props}
      >
        {children}
      </div>
    </SidebarContext.Provider>
  );
}

export function Sidebar({
  className,
  children,
  label,
  description,
}: ComponentProps<"div"> & { label: string; description?: string }) {
  const { isMobile, state, openMobile, setOpenMobile } = useSidebar();
  if (isMobile) {
    return (
      <Sheet open={openMobile} onOpenChange={setOpenMobile}>
        <SheetContent side="left" className="w-(--sidebar-width) p-0 text-sidebar-foreground">
          <SheetTitle className="sr-only">{label}</SheetTitle>
          <SheetDescription className="sr-only">{description ?? label}</SheetDescription>
          <div className="flex h-full w-full flex-col" data-mobile="true">
            {children}
          </div>
        </SheetContent>
      </Sheet>
    );
  }
  return (
    <div
      className="group peer hidden text-sidebar-foreground md:block"
      data-state={state}
      data-collapsible={state === "collapsed" ? "icon" : ""}
    >
      {/* Reserves the width in the flow; the sidebar itself is fixed. */}
      <div className="relative w-(--sidebar-width) bg-transparent transition-[width] duration-200 ease-out group-data-[collapsible=icon]:w-(--sidebar-width-icon)" />
      <aside
        aria-label={label}
        className={cn(
          "fixed inset-y-0 left-0 z-10 hidden h-svh w-(--sidebar-width) border-r border-sidebar-border bg-sidebar transition-[width] duration-200 ease-out md:flex group-data-[collapsible=icon]:w-(--sidebar-width-icon)",
          className,
        )}
      >
        <div className="flex h-full w-full flex-col">{children}</div>
      </aside>
    </div>
  );
}

export function SidebarTrigger({
  className,
  label,
  ...props
}: ComponentProps<typeof Button> & { label: string }) {
  const { toggle } = useSidebar();
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      className={cn("text-muted-foreground", className)}
      onClick={toggle}
      aria-label={label}
      {...props}
    >
      <PanelLeft />
    </Button>
  );
}

export function SidebarInset({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn("relative flex min-w-0 flex-1 flex-col bg-background", className)}
      {...props}
    />
  );
}

export function SidebarHeader({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("flex flex-col gap-2 p-2", className)} {...props} />;
}

export function SidebarFooter({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn("mt-auto flex flex-col gap-2 border-t border-sidebar-border p-2", className)}
      {...props}
    />
  );
}

export function SidebarContent({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex min-h-0 flex-1 flex-col gap-1 overflow-auto group-data-[collapsible=icon]:overflow-hidden",
        className,
      )}
      {...props}
    />
  );
}

export function SidebarGroup({ className, ...props }: ComponentProps<"div">) {
  return (
    <div className={cn("relative flex w-full min-w-0 flex-col px-2 py-1", className)} {...props} />
  );
}

export function SidebarGroupLabel({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex h-7 shrink-0 items-center px-2 text-xs font-medium text-subtle-foreground transition-[margin,opacity] duration-200 group-data-[collapsible=icon]:-mt-7 group-data-[collapsible=icon]:opacity-0",
        className,
      )}
      {...props}
    />
  );
}

export function SidebarMenu({ className, ...props }: ComponentProps<"ul">) {
  return <ul className={cn("flex w-full min-w-0 flex-col gap-px", className)} {...props} />;
}

export function SidebarMenuItem({ className, ...props }: ComponentProps<"li">) {
  return <li className={cn("group/menu-item relative", className)} {...props} />;
}

export function SidebarMenuButton({
  asChild = false,
  isActive = false,
  tooltip,
  className,
  ...props
}: ComponentProps<"button"> & { asChild?: boolean; isActive?: boolean; tooltip?: string }) {
  const Comp = asChild ? Slot.Root : "button";
  const { state, isMobile } = useSidebar();
  const button = (
    <Comp
      data-active={isActive || undefined}
      className={cn(
        "peer/menu-button relative flex h-8 w-full items-center gap-2.5 overflow-hidden rounded-md px-2 text-left text-[13px] outline-none transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring disabled:pointer-events-none disabled:opacity-50 data-[active]:bg-sidebar-accent data-[active]:font-medium data-[active]:text-sidebar-accent-foreground group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:p-2 [&>span:last-child]:truncate [&>svg]:size-4 [&>svg]:shrink-0 [&>svg]:text-muted-foreground data-[active]:[&>svg]:text-foreground",
        className,
      )}
      {...props}
    />
  );
  if (!tooltip || state !== "collapsed" || isMobile) return button;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{button}</TooltipTrigger>
      <TooltipContent side="right" align="center">
        {tooltip}
      </TooltipContent>
    </Tooltip>
  );
}

export function SidebarMenuBadge({ className, ...props }: ComponentProps<"span">) {
  return (
    <span
      className={cn(
        "tabular pointer-events-none absolute top-1.5 right-1.5 flex h-5 min-w-5 items-center justify-center rounded-md px-1 text-xs font-medium select-none group-data-[collapsible=icon]:hidden",
        className,
      )}
      {...props}
    />
  );
}

export function SidebarRail({ label }: { label: string }) {
  const { toggle } = useSidebar();
  return (
    <button
      aria-label={label}
      tabIndex={-1}
      onClick={toggle}
      title={label}
      className="absolute inset-y-0 -right-2 z-20 hidden w-4 cursor-ew-resize after:absolute after:inset-y-0 after:left-1/2 after:w-px hover:after:bg-sidebar-border md:flex"
    />
  );
}
