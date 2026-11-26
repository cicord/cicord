import { partialPrivacyReport, type PrivacyReport } from "@cicord/client";
import { useCicord, useCicordQuery } from "@cicord/react";
import { useState } from "react";
import { useTranslation } from "../i18n/index.ts";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "./ui/alert-dialog.tsx";
import { Button } from "./ui/button.tsx";
import { toastError } from "./errors.tsx";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card.tsx";
import { Input } from "./ui/input.tsx";
import { Label } from "./ui/label.tsx";

export function MemberData({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation(),
    { client } = useCicord();
  const [id, setID] = useState(""),
    [pending, setPending] = useState(false);
  const [report, setReport] = useState<PrivacyReport>(),
    [error, setError] = useState("");
  // A Discord user snowflake: shorter numbers (counts, votes) are never member IDs.
  const valid = /^[1-9][0-9]{16,19}$/.test(id);
  async function run(action: "export" | "erase") {
    setError("");
    setReport(undefined);
    setPending(true);
    try {
      setReport(await client.admin.memberData(serverKey, action, id));
    } catch (e) {
      // An erasure that failed part-way returns what already ran: show it and offer a retry.
      const partial = partialPrivacyReport(e);
      if (partial) setReport(partial);
      setError(
        partial ? t("memberData.failedPartway") : e instanceof Error ? e.message : String(e),
      );
    } finally {
      setPending(false);
    }
  }
  function download() {
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(report, null, 2)], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = `member-data-${serverKey}-${id}.json`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  return (
    <Card aria-labelledby="member-data-title">
      <CardHeader className="flex-col gap-0.5">
        <CardTitle id="member-data-title">{t("memberData.title")}</CardTitle>
        <CardDescription>{t("memberData.description")}</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        <div className="grid max-w-sm gap-1.5">
          <Label htmlFor="privacy-member">{t("memberData.label")}</Label>
          <Input
            id="privacy-member"
            inputMode="numeric"
            autoComplete="off"
            maxLength={20}
            value={id}
            disabled={pending}
            onChange={(e) => {
              setID(e.target.value.trim());
              setReport(undefined);
              setError("");
            }}
          />
        </div>
        <div className="flex flex-wrap gap-2">
          <Button disabled={!valid || pending} onClick={() => void run("export")}>
            {t("memberData.export")}
          </Button>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="outline" disabled={!valid || pending}>
                {t("memberData.erase")}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogTitle>{t("memberData.question", { id })}</AlertDialogTitle>
              <AlertDialogDescription>{t("memberData.effect")}</AlertDialogDescription>
              <AlertDialogFooter>
                <AlertDialogCancel>{t("memberData.cancel")}</AlertDialogCancel>
                <AlertDialogAction onClick={() => void run("erase")}>
                  {t("memberData.erase")}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
        {error && (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        )}
        {pending && (
          <p role="status" className="text-sm">
            {t("memberData.working")}
          </p>
        )}
        {report && (
          <div className="grid gap-2 border-t pt-3">
            <p role="status" className="text-sm font-medium">
              {t(report.complete ? "memberData.complete" : "memberData.partial")}
            </p>
            <ul className="grid gap-1 text-sm">
              {report.plugins.map((p) => (
                <li key={p.instance} className="flex flex-wrap justify-between gap-x-4">
                  <span>{p.instance}</span>
                  <span className="text-muted-foreground">
                    {t(`memberData.status.${p.status}`)}
                  </span>
                </li>
              ))}
            </ul>
            {report.plugins.some((p) => p.status === "orphaned") && (
              <p className="text-sm text-muted-foreground">{t("memberData.orphanedHint")}</p>
            )}
            <Button variant="outline" className="justify-self-start" onClick={download}>
              {t("memberData.download")}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

/**
 * Data left by installations removed from the manifest. Their hooks cannot run, so a
 * member erasure stays incomplete until the admin purges it (typing the name to confirm).
 */
export function OrphanedData({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation(),
    { client } = useCicord();
  const orphans = useCicordQuery(["admin", "server", serverKey, "orphaned-data"], (signal) =>
    client.admin.orphanedData(serverKey, { signal }),
  );
  const [target, setTarget] = useState(""),
    [typed, setTyped] = useState(""),
    [purging, setPurging] = useState(false);
  async function purge(instance: string) {
    setPurging(true);
    try {
      await client.admin.purgeOrphanedData(serverKey, instance);
      await orphans.refetch();
    } catch (e) {
      toastError(e, t("orphanedData.failed"));
    } finally {
      setPurging(false);
      setTarget("");
      setTyped("");
    }
  }
  const items = orphans.data ?? [];
  if (!items.length) return null;
  return (
    <Card aria-labelledby="orphaned-data-title">
      <CardHeader className="flex-col gap-0.5">
        <CardTitle id="orphaned-data-title">{t("orphanedData.title")}</CardTitle>
        <CardDescription>{t("orphanedData.description")}</CardDescription>
      </CardHeader>
      <CardContent>
        <ul className="grid gap-2 text-sm">
          {items.map((o) => (
            <li key={o.instance} className="flex flex-wrap items-center justify-between gap-x-4">
              <span className="grid">
                <span className="font-medium">{o.instance}</span>
                <span className="text-muted-foreground">
                  {[
                    t("orphanedData.records", { count: o.records }),
                    t("orphanedData.rankings", { count: o.rankings }),
                    t("orphanedData.index", { count: o.index }),
                    t("orphanedData.tasks", { count: o.tasks }),
                  ].join(", ")}
                </span>
              </span>
              <AlertDialog
                open={target === o.instance}
                onOpenChange={(open) => {
                  setTarget(open ? o.instance : "");
                  setTyped("");
                }}
              >
                <AlertDialogTrigger asChild>
                  <Button variant="outline" disabled={purging}>
                    {t("orphanedData.purge")}
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogTitle>
                    {t("orphanedData.question", { instance: o.instance })}
                  </AlertDialogTitle>
                  <AlertDialogDescription>{t("orphanedData.effect")}</AlertDialogDescription>
                  <div className="grid gap-1.5">
                    <Label htmlFor="orphan-confirm">
                      {t("orphanedData.confirmLabel", { instance: o.instance })}
                    </Label>
                    <Input
                      id="orphan-confirm"
                      autoComplete="off"
                      value={typed}
                      onChange={(e) => setTyped(e.target.value.trim())}
                    />
                  </div>
                  <AlertDialogFooter>
                    <AlertDialogCancel>{t("memberData.cancel")}</AlertDialogCancel>
                    <AlertDialogAction
                      disabled={typed !== o.instance || purging}
                      onClick={() => void purge(o.instance)}
                    >
                      {t("orphanedData.purge")}
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}
