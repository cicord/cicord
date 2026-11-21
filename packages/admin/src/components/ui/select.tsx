import { Check, ChevronDown } from "lucide-react";
import { Select as P } from "radix-ui";
import type { ComponentProps } from "react";
import { cn } from "../../lib/utils.ts";

export const Select = P.Root;
export const SelectValue = P.Value;
export const SelectGroup = P.Group;

export function SelectTrigger({ className, children, ...props }: ComponentProps<typeof P.Trigger>) {
  return (
    <P.Trigger
      className={cn(
        "flex h-8 w-full items-center justify-between gap-2 rounded-md border border-input bg-transparent px-2.5 text-sm whitespace-nowrap outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/25 disabled:cursor-not-allowed disabled:opacity-50 data-[placeholder]:text-subtle-foreground dark:bg-surface-sunken [&>span]:truncate",
        className,
      )}
      {...props}
    >
      {children}
      <P.Icon asChild>
        <ChevronDown className="size-4 opacity-60" />
      </P.Icon>
    </P.Trigger>
  );
}

export function SelectContent({
  className,
  children,
  position = "popper",
  ...props
}: ComponentProps<typeof P.Content>) {
  return (
    <P.Portal>
      <P.Content
        position={position}
        className={cn(
          "relative z-50 max-h-(--radix-select-content-available-height) min-w-32 overflow-hidden rounded-lg border bg-popover text-popover-foreground shadow-pop data-[state=open]:animate-in data-[state=open]:fade-in-0",
          position === "popper" && "w-full min-w-(--radix-select-trigger-width) translate-y-1",
          className,
        )}
        {...props}
      >
        <P.Viewport className="p-1">{children}</P.Viewport>
      </P.Content>
    </P.Portal>
  );
}

export function SelectItem({ className, children, ...props }: ComponentProps<typeof P.Item>) {
  return (
    <P.Item
      className={cn(
        "relative flex w-full cursor-default items-center gap-2 rounded-[5px] py-1.5 pr-8 pl-2 text-[13px] outline-none select-none focus:bg-accent data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
        className,
      )}
      {...props}
    >
      <span className="absolute right-2 flex size-3.5 items-center justify-center">
        <P.ItemIndicator>
          <Check className="size-4" />
        </P.ItemIndicator>
      </span>
      <P.ItemText>{children}</P.ItemText>
    </P.Item>
  );
}
