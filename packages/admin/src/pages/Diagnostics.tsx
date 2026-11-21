/** Diagnostics: the setup wizard's live checklist, re-runnable at any time. */
import { useCodi } from "@codi/react";
import { RotateCw } from "lucide-react";
import { Page, PageHeader, Time } from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { useTranslation } from "../i18n/index.ts";
import { diagnostics } from "../setup/api.ts";
import { usePoll } from "../setup/hooks.ts";
import { Checklist, SetupError } from "../setup/kit.tsx";

export function DiagnosticsPage({ serverKey }: { serverKey: string }) {
  const { t } = useTranslation();
  const { client } = useCodi();
  const rep = usePoll((signal) => diagnostics(client, serverKey, signal), 15_000);
  return (
    <Page className="max-w-3xl">
      <PageHeader
        title={t("diagnostics.title")}
        description={t("diagnostics.description")}
        actions={
          <Button variant="outline" size="sm" onClick={rep.refresh}>
            <RotateCw />
            {t("diagnostics.run")}
          </Button>
        }
      />
      {rep.error ? <SetupError error={rep.error} /> : null}
      {rep.data ? (
        <>
          <p className="mb-2 text-xs text-muted-foreground">
            {t("diagnostics.ranAt")} <Time iso={rep.data.ranAt} /> · {t("diagnostics.sinceStart")}
          </p>
          <Checklist checks={rep.data.checks} label={t("diagnostics.title")} />
        </>
      ) : null}
    </Page>
  );
}
