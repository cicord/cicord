import type { PrivacyReport } from "@cicord/client";
import { useCicord } from "@cicord/react";
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
  const valid = /^[1-9][0-9]{0,19}$/.test(id);
  async function run(action: "export" | "erase") {
    setError("");
    setReport(undefined);
    setPending(true);
    try {
      setReport(await client.admin.memberData(serverKey, action, id));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
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
            <Button variant="outline" className="justify-self-start" onClick={download}>
              {t("memberData.download")}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
