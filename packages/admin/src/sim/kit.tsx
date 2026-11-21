/**
 * The simulator's building blocks, on top of the console design system (components/ui).
 * Thin adapters keep the simulator's call sites compact (tone names, icon + label buttons).
 */
import {
  ExternalLink,
  Eye,
  Hash,
  Loader2,
  LogOut,
  Mail,
  Play,
  Plus,
  Send,
  Slash,
  User,
  Volume2,
  X,
  type LucideProps,
} from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import { toast } from "sonner";
import { LoadingRows, UserAvatar } from "../components/common.tsx";
import { Badge as UiBadge, type BadgeTone } from "../components/ui/badge.tsx";
import { Button as UiButton } from "../components/ui/button.tsx";
import { StatusDot, type Tone as StatusTone } from "../components/status.tsx";
import { describeError } from "../lib/errors.ts";

export { EmptyState } from "../components/common.tsx";
export { ErrorState } from "../components/errors.tsx";

type IconProps = LucideProps & { size?: number };
const sized =
  (Icon: (p: LucideProps) => ReactNode) =>
  ({ size = 16, ...rest }: IconProps) => (
    <Icon width={size} height={size} aria-hidden="true" {...rest} />
  );

export const IconExternal = sized(ExternalLink);
export const IconEye = sized(Eye);
export const IconHash = sized(Hash);
export const IconLeave = sized(LogOut);
export const IconMail = sized(Mail);
export const IconPlay = sized(Play);
export const IconPlus = sized(Plus);
export const IconSend = sized(Send);
export const IconSlash = sized(Slash);
export const IconUser = sized(User);
export const IconVolume = sized(Volume2);
export const IconX = sized(X);

export function Avatar({
  name,
  url,
  size = 24,
  bot,
}: {
  name: string;
  url?: string;
  size?: number;
  bot?: boolean;
}) {
  return (
    <UserAvatar
      name={name}
      url={url ?? ""}
      size={size}
      className={bot ? "avatar ring-1 ring-brand/40" : "avatar"}
    />
  );
}

export function Spinner({ size = 16, label }: { size?: number; label?: string }) {
  return (
    <Loader2
      className="animate-spin text-muted-foreground"
      style={{ width: size, height: size }}
      role={label ? "status" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    />
  );
}

export function Skeleton({ lines = 3 }: { lines?: number }) {
  return <LoadingRows rows={lines} />;
}

export type Tone = "ok" | "warn" | "bad" | "info" | "neutral" | "violet" | "accent";
const BADGE: Record<Tone, BadgeTone> = {
  ok: "success",
  warn: "warning",
  bad: "danger",
  info: "info",
  neutral: "neutral",
  violet: "violet",
  accent: "brand",
};
const DOT: Record<Tone, StatusTone> = {
  ok: "success",
  warn: "warning",
  bad: "danger",
  info: "info",
  neutral: "neutral",
  violet: "violet",
  accent: "info",
};

export function Badge({
  tone = "neutral",
  children,
  dot,
  title,
}: {
  tone?: Tone;
  children: ReactNode;
  dot?: boolean;
  title?: string;
}) {
  return (
    <UiBadge tone={BADGE[tone]} title={title}>
      {dot ? <StatusDot tone={DOT[tone]} /> : null}
      {children}
    </UiBadge>
  );
}

export function Button({
  variant = "secondary",
  size = "md",
  icon,
  children,
  ...rest
}: Omit<ComponentProps<typeof UiButton>, "variant" | "size"> & {
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md";
  icon?: ReactNode;
}) {
  const v =
    variant === "primary"
      ? "default"
      : variant === "secondary"
        ? "outline"
        : variant === "danger"
          ? "destructive"
          : "ghost";
  return (
    <UiButton variant={v} size={size === "sm" ? "sm" : "default"} {...rest}>
      {rest.loading ? null : icon}
      {children}
    </UiButton>
  );
}

export function errorMessage(error: unknown): string {
  const d = describeError(error);
  return d.hint ? `${d.message} ${d.hint}` : d.message;
}

/** Same call shape as before ({tone, title, text}), rendered by sonner. */
export function useToast(): {
  push: (t: { tone: "ok" | "bad" | "info"; title: string; text?: ReactNode }) => void;
} {
  return {
    push: ({ tone, title, text }) => {
      const opts = text ? { description: text } : undefined;
      if (tone === "ok") toast.success(title, opts);
      else if (tone === "bad") toast.error(title, opts);
      else toast.info(title, opts);
    },
  };
}
