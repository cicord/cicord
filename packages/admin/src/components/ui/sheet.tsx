import { Dialog as P } from "radix-ui";
import type { ComponentProps } from "react";
import { cn } from "../../lib/utils.ts";
import { overlayClass } from "./dialog.tsx";

export const Sheet = P.Root;
export const SheetTrigger = P.Trigger;
export const SheetClose = P.Close;

export function SheetContent({
  className,
  side = "left",
  ...props
}: ComponentProps<typeof P.Content> & { side?: "left" | "right" }) {
  return (
    <P.Portal>
      <P.Overlay className={overlayClass} />
      <P.Content
        className={cn(
          "fixed inset-y-0 z-50 flex h-full w-72 flex-col bg-sidebar shadow-pop transition ease-out data-[state=closed]:animate-out data-[state=closed]:duration-200 data-[state=open]:animate-in data-[state=open]:duration-200",
          side === "left"
            ? "left-0 border-r data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left"
            : "right-0 border-l data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right",
          className,
        )}
        {...props}
      />
    </P.Portal>
  );
}

export const SheetTitle = P.Title;
export const SheetDescription = P.Description;
