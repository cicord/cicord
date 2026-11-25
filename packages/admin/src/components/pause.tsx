/**
 * Emergency switch: pause enforcement on a server (nothing is applied to it until it is
 * resumed), the banner every page of a paused server shows, and the resume flow (the
 * pending plan first, then confirm). Moderators and admins of the server may pause and
 * resume; only admins can resume and sync at once (a sync is admin-only).
 */
import type { PauseInfo } from "@cicord/client";
import { useCicord } from "@cicord/react";
import { CirclePause, CirclePlay } from "lucide-react";
import { createContext, useContext, useMemo, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { usePauseState } from "../api.ts";
import { useWorkspace } from "../context.tsx";
import { useTranslation } from "../i18n/index.ts";
import { Link } from "../router.tsx";
import { PlanView } from "../pages/Sync.tsx";
import { LoadingRows, Time } from "./common.tsx";
import { Callout, ErrorState, toastError } from "./errors.tsx";
import { Badge } from "./ui/badge.tsx";
import { Button } from "./ui/button.tsx";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./ui/dialog.tsx";
import { Input } from "./ui/input.tsx";
import { Label } from "./ui/label.tsx";
import { Tip } from "./ui/tooltip.tsx";

/** Longest pause reason the runtime accepts. */
const MAX_REASON = 500;

interface PauseControls {
  /** Opens the pause dialog of the current server. */
  openPause: () => void;
  /** Opens the resume dialog (pending plan, then confirm). */
  openResume: () => void;
}

const PauseContext = createContext<PauseControls>({ openPause: () => {}, openResume: () => {} });

export function usePauseControls(): PauseControls {
  return useContext(PauseContext);
}

/** Who paused: the Discord display name, else the actor. */
function who(p: PauseInfo): string {
  return p.byName || p.by;
}

/** Holds the pause and resume dialogs of the current server (one per shell). */
export function PauseProvider({ children }: { children: ReactNode }) {
  const ws = useWorkspace();
  const [dialog, setDialog] = useState<"pause" | "resume" | null>(null);
  const controls = useMemo<PauseControls>(
    () => ({ openPause: () => setDialog("pause"), openResume: () => setDialog("resume") }),
    [],
  );
  const key = ws.serverKey;
  return (
    <PauseContext.Provider value={controls}>
      {children}
      {key ? (
        <>
          <PauseDialog
            serverKey={key}
            open={dialog === "pause"}
            onOpenChange={(o) => setDialog(o ? "pause" : null)}
          />
          <ResumeDialog
            serverKey={key}
            open={dialog === "resume"}
            onOpenChange={(o) => setDialog(o ? "resume" : null)}
          />
        </>
      ) : null}
    </PauseContext.Provider>
  );
}

/** "Pause enforcement" or, on a paused server, "Resume…" (Overview header). */
export function PauseButton({ size }: { size?: "sm" | "default" }) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { openPause, openResume } = usePauseControls();
  if (!ws.server || (ws.role !== "admin" && ws.role !== "moderator")) return null;
  return ws.server.paused ? (
    <Button size={size} onClick={openResume}>
      <CirclePlay />
      {t("pause.resume")}
    </Button>
  ) : (
    <Button size={size} variant="outline" onClick={openPause}>
      <CirclePause className="text-warning" />
      {t("pause.pause")}
    </Button>
  );
}

/** "Paused" next to the sync and health badges. */
export function PausedBadge() {
  const { t } = useTranslation();
  return (
    <Tip content={t("pause.badgeHint")}>
      <Badge tone="warning" tabIndex={0}>
        <CirclePause aria-hidden="true" />
        {t("pause.badge")}
      </Badge>
    </Tip>
  );
}

/** The banner of every page of a paused server. */
export function PauseBanner() {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { openResume } = usePauseControls();
  const s = ws.server;
  const p = s?.paused;
  if (!s || !p) return null;
  const drift = s.drift.length;
  return (
    <div className="mx-auto w-full max-w-6xl px-4 pt-4 md:px-8" data-testid="pause-banner">
      <Callout
        tone="warning"
        title={t("pause.banner.title")}
        meta={
          <span>
            {t("pause.banner.by", { name: who(p) })} · <Time iso={p.at} />
          </span>
        }
        action={
          <Button size="sm" onClick={openResume}>
            <CirclePlay />
            {t("pause.resume")}
          </Button>
        }
      >
        {p.reason ? (
          <p className="font-medium">
            {t("pause.banner.reason")} {p.reason}
          </p>
        ) : null}
        <p>
          {t("pause.banner.text")}{" "}
          {drift === 0 ? (
            t("pause.banner.noDrift")
          ) : (
            <Link
              to={`/servers/${s.key}/drift`}
              className="font-medium text-warning hover:underline"
            >
              {t("pause.banner.drift", { count: drift })}
            </Link>
          )}
        </p>
      </Callout>
    </div>
  );
}

function PauseDialog({
  serverKey,
  open,
  onOpenChange,
}: {
  serverKey: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const { client, cache } = useCicord();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    setBusy(true);
    try {
      const res = await client.admin.pause(serverKey, { reason: reason.trim() });
      toast.success(
        res.changed ? t("pause.toast.paused") : t("pause.toast.already", { name: who(res.paused) }),
      );
      setReason("");
      onOpenChange(false);
    } catch (e) {
      toastError(e, t("pause.toast.pauseFailed"));
    } finally {
      void cache.invalidate(["admin"]);
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent>
        <form
          className="contents"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <DialogHeader>
            <DialogTitle>{t("pause.dialog.title")}</DialogTitle>
            <DialogDescription>{t("pause.dialog.text")}</DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pause-reason">{t("pause.dialog.reason")}</Label>
            <Input
              id="pause-reason"
              value={reason}
              maxLength={MAX_REASON}
              placeholder={t("pause.dialog.reasonPlaceholder")}
              onChange={(e) => setReason(e.target.value)}
              autoFocus
            />
          </div>
          <DialogFooter>
            <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {busy ? null : <CirclePause />}
              {t("pause.pause")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ResumeDialog({
  serverKey,
  open,
  onOpenChange,
}: {
  serverKey: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [busy, setBusy] = useState<"resume" | "sync" | null>(null);
  return (
    <Dialog open={open} onOpenChange={(o) => busy === null && onOpenChange(o)}>
      <DialogContent className="sm:max-w-3xl">
        {/* Mounted while open only: the pause state is fetched when the dialog opens. */}
        <ResumeContent
          serverKey={serverKey}
          busy={busy}
          setBusy={setBusy}
          close={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  );
}

function ResumeContent({
  serverKey,
  busy,
  setBusy,
  close,
}: {
  serverKey: string;
  busy: "resume" | "sync" | null;
  setBusy: (busy: "resume" | "sync" | null) => void;
  close: () => void;
}) {
  const { t } = useTranslation();
  const ws = useWorkspace();
  const { client, cache } = useCicord();
  const state = usePauseState(serverKey);
  const run = async (sync: boolean) => {
    setBusy(sync ? "sync" : "resume");
    try {
      if (sync) {
        const res = await client.admin.sync(serverKey, { resume: true });
        toast.success(t("pause.toast.resumedSynced", { count: res.applied }));
      } else {
        const res = await client.admin.resume(serverKey);
        toast.success(res.resumed ? t("pause.toast.resumed") : t("pause.toast.notPaused"));
      }
      close();
    } catch (e) {
      toastError(e, t("pause.toast.resumeFailed"));
    } finally {
      void cache.invalidate(["admin"]);
      setBusy(null);
    }
  };
  const p = state.data?.paused;
  return (
    <>
      <DialogHeader>
        <DialogTitle>{t("pause.resumeDialog.title")}</DialogTitle>
        <DialogDescription>{t("pause.resumeDialog.text")}</DialogDescription>
      </DialogHeader>
      <div className="max-h-[55vh] overflow-y-auto">
        {state.isLoading ? (
          <LoadingRows rows={4} />
        ) : !state.data ? (
          <ErrorState error={state.error} onRetry={() => void state.refetch()} />
        ) : (
          <div className="flex flex-col gap-3">
            {p ? (
              <p className="text-[13px] text-muted-foreground">
                {t("pause.banner.by", { name: who(p) })} · <Time iso={p.at} />
                {p.reason ? ` · ${p.reason}` : ""}
              </p>
            ) : (
              <Callout tone="info" title={t("pause.resumeDialog.notPaused")} />
            )}
            <h3 className="text-sm font-semibold">{t("pause.resumeDialog.plan")}</h3>
            {state.data.plan ? (
              <PlanView plan={state.data.plan} />
            ) : (
              <Callout tone="warning" title={t("pause.resumeDialog.noPlan")}>
                {state.data.planError}
              </Callout>
            )}
          </div>
        )}
      </div>
      <DialogFooter>
        <Button variant="outline" disabled={busy !== null} onClick={close}>
          {t("common.cancel")}
        </Button>
        {ws.isAdmin ? (
          <Button
            variant="outline"
            loading={busy === "sync"}
            disabled={busy !== null || !p}
            onClick={() => void run(true)}
          >
            {t("pause.resumeDialog.resumeSync")}
          </Button>
        ) : null}
        <Button
          loading={busy === "resume"}
          disabled={busy !== null || !p}
          onClick={() => void run(false)}
        >
          {busy === "resume" ? null : <CirclePlay />}
          {t("pause.resumeDialog.confirm")}
        </Button>
      </DialogFooter>
    </>
  );
}
