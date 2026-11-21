import { Toaster as Sonner, type ToasterProps } from "sonner";

export function Toaster({ theme, ...props }: ToasterProps) {
  return (
    <Sonner
      theme={theme}
      className="toaster group"
      position="bottom-right"
      closeButton
      toastOptions={{
        classNames: {
          toast:
            "group toast !rounded-lg !border !border-border !bg-popover !text-popover-foreground !shadow-pop !font-sans",
          title: "!text-[13px] !font-semibold",
          description: "!text-[13px] !text-muted-foreground",
          actionButton: "!bg-primary !text-primary-foreground",
          cancelButton: "!bg-muted !text-muted-foreground",
          closeButton: "!bg-popover !border-border !text-muted-foreground",
          error: "[&_[data-icon]]:!text-danger",
          success: "[&_[data-icon]]:!text-success",
          info: "[&_[data-icon]]:!text-info",
          warning: "[&_[data-icon]]:!text-warning",
        },
      }}
      {...props}
    />
  );
}
