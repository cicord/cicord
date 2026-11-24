/** Console settings: appearance, language, account and runtime information. */
import { useLogout } from "@cicord/react";
import { LogOut, Monitor, Moon, Sun } from "lucide-react";
import type { ReactNode } from "react";
import { Mono, Page, PageHeader, Sha, Time, UserAvatar } from "../components/common.tsx";
import { Badge } from "../components/ui/badge.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card, CardDescription, CardHeader, CardTitle } from "../components/ui/card.tsx";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select.tsx";
import { ToggleGroup, ToggleGroupItem } from "../components/ui/toggle-group.tsx";
import { RoleBadge, useAdminConfig, useViewer, useWorkspace } from "../context.tsx";
import { LANGUAGES, setLanguage, useTranslation, type Language } from "../i18n/index.ts";
import { navigate } from "../router.tsx";
import { useTheme, type ThemePreference } from "../shell/theme.tsx";

export function SettingsPage() {
  const { t, i18n } = useTranslation();
  const { preference, setPreference } = useTheme();
  const me = useViewer();
  const ws = useWorkspace();
  const config = useAdminConfig();
  const logout = useLogout();
  const name = me.user.displayName || me.user.username;
  const st = ws.status;
  return (
    <Page className="max-w-3xl">
      <PageHeader title={t("settings.title")} description={t("settings.description")} />

      <Section
        title={t("settings.appearance.title")}
        description={t("settings.appearance.description")}
      >
        <Row label={t("settings.theme.label")} htmlFor="theme">
          <ToggleGroup
            id="theme"
            type="single"
            value={preference}
            onValueChange={(v) => v && setPreference(v as ThemePreference)}
            aria-label={t("settings.theme.label")}
          >
            <ToggleGroupItem value="system">
              <Monitor className="size-3.5" />
              {t("settings.theme.system")}
            </ToggleGroupItem>
            <ToggleGroupItem value="light">
              <Sun className="size-3.5" />
              {t("settings.theme.light")}
            </ToggleGroupItem>
            <ToggleGroupItem value="dark">
              <Moon className="size-3.5" />
              {t("settings.theme.dark")}
            </ToggleGroupItem>
          </ToggleGroup>
        </Row>
        <Row
          label={t("settings.language.label")}
          htmlFor="language"
          help={t("settings.language.help")}
        >
          <Select value={i18n.language} onValueChange={(v) => setLanguage(v as Language)}>
            <SelectTrigger id="language" className="w-48" aria-label={t("settings.language.label")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {LANGUAGES.map((l) => (
                <SelectItem key={l.id} value={l.id}>
                  {l.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Row>
      </Section>

      <Section title={t("settings.account.title")} description={t("settings.account.description")}>
        <div className="flex items-center gap-3 px-4 py-3">
          <UserAvatar name={name} url={me.user.avatarUrl} size={36} />
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-medium">{name}</p>
            <p className="text-xs text-muted-foreground">
              @{me.user.username} · <Mono className="tabular">{me.user.id}</Mono>
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => void logout().then(() => navigate("/", { replace: true }))}
          >
            <LogOut />
            {t("user.logout")}
          </Button>
        </div>
        {me.servers.map((s) => (
          <Row key={s.key} label={s.name}>
            <RoleBadge role={s.role} />
          </Row>
        ))}
      </Section>

      <Section title={t("settings.runtime.title")} description={t("settings.runtime.description")}>
        <Row label={t("settings.runtime.version")}>
          <Mono className="text-foreground">{st?.version ?? "—"}</Mono>
        </Row>
        <Row label={t("settings.runtime.mode")}>
          <span className="flex items-center gap-2">
            <Mono className="text-foreground">cicord {st?.mode ?? "—"}</Mono>
            {st?.simulator ? <Badge tone="violet">{t("settings.runtime.simulator")}</Badge> : null}
            {config.mock ? <Badge tone="violet">{t("app.mock")}</Badge> : null}
          </span>
        </Row>
        <Row label={t("settings.runtime.revision")}>
          {st?.revision ? (
            <span className="flex min-w-0 items-center gap-2">
              <Sha id={st.revision.id} icon className="text-foreground" />
              <span className="truncate text-muted-foreground">{st.revision.author}</span>
              <Time iso={st.revision.time} className="text-muted-foreground" />
            </span>
          ) : (
            <span className="text-muted-foreground">{t("overview.noRevision")}</span>
          )}
        </Row>
      </Section>
    </Page>
  );
}

function Section({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader className="flex-col gap-0.5">
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <div className="divide-y">{children}</div>
    </Card>
  );
}

function Row({
  label,
  children,
  help,
  htmlFor,
}: {
  label: string;
  children: ReactNode;
  help?: string;
  htmlFor?: string;
}) {
  return (
    <div className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
      <div>
        <label htmlFor={htmlFor} className="text-[13px] font-medium">
          {label}
        </label>
        {help ? <p className="text-xs text-muted-foreground">{help}</p> : null}
      </div>
      <div className="min-w-0">{children}</div>
    </div>
  );
}
