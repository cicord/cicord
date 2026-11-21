import type { ComponentProps } from "react";
import { cn } from "../../lib/utils.ts";

export function Input({ className, type, ...props }: ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        "flex h-8 w-full min-w-0 rounded-md border border-input bg-transparent px-2.5 text-sm transition-[color,box-shadow,border-color] outline-none placeholder:text-subtle-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/25 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-danger dark:bg-surface-sunken",
        className,
      )}
      {...props}
    />
  );
}
