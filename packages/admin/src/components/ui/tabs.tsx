import { Tabs as P } from "radix-ui";
import type { ComponentProps } from "react";
import { cn } from "../../lib/utils.ts";

export const Tabs = P.Root;

export function TabsList({ className, ...props }: ComponentProps<typeof P.List>) {
  return (
    <P.List
      className={cn("inline-flex h-9 items-center gap-1 border-b text-muted-foreground", className)}
      {...props}
    />
  );
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof P.Trigger>) {
  return (
    <P.Trigger
      className={cn(
        "relative -mb-px inline-flex h-9 items-center gap-1.5 border-b-2 border-transparent px-2 text-[13px] font-medium whitespace-nowrap transition-colors outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50 data-[state=active]:border-foreground data-[state=active]:text-foreground",
        className,
      )}
      {...props}
    />
  );
}

export function TabsContent({ className, ...props }: ComponentProps<typeof P.Content>) {
  return <P.Content className={cn("outline-none", className)} {...props} />;
}
