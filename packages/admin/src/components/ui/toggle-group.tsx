import { ToggleGroup as P } from "radix-ui";
import type { ComponentProps } from "react";
import { cn } from "../../lib/utils.ts";

/** Segmented control (single choice). Items are exposed as radios. */
export function ToggleGroup({ className, ...props }: ComponentProps<typeof P.Root>) {
  return (
    <P.Root
      className={cn(
        "inline-flex items-center gap-0.5 rounded-md border bg-surface-sunken p-0.5",
        className,
      )}
      {...props}
    />
  );
}

export function ToggleGroupItem({ className, ...props }: ComponentProps<typeof P.Item>) {
  return (
    <P.Item
      className={cn(
        "inline-flex h-6 items-center gap-1.5 rounded-[5px] px-2 text-xs font-medium text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50 data-[state=on]:bg-background data-[state=on]:text-foreground data-[state=on]:shadow-sm dark:data-[state=on]:bg-accent",
        className,
      )}
      {...props}
    />
  );
}
