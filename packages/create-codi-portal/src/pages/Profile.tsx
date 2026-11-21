import type { LeaderboardEntry } from "@codi/client";
import { useLogout, useMe, useMember, usePluginQuery } from "@codi/react";
import { Button, MemberCard, Notice, ProgressBar, Spinner } from "@codi/ui";
import { useT } from "../i18n/index.tsx";
import { SignIn, useNumber, usePages, usePlugin } from "../portal.tsx";
import { Link, navigate } from "../router.tsx";

export function Profile() {
  const t = useT();
  const me = useMe();
  const member = useMember();
  const logout = useLogout();
  const pages = usePages();

  if (me.isLoading || (me.data && member.isLoading)) {
    return (
      <div className="portal-loading portal-loading--inline">
        <Spinner size="lg" label={t.loading} />
      </div>
    );
  }
  const user = me.data?.user;
  if (!user) {
    return (
      <div className="onboarding-gate">
        <h1>{t.me.loginTitle}</h1>
        <SignIn />
      </div>
    );
  }
  const data = member.data;
  const joined = data?.member.joinedAt ? new Date(data.member.joinedAt) : undefined;
  const since =
    joined && !Number.isNaN(joined.getTime())
      ? t.me.since(new Intl.DateTimeFormat(t.locale, { dateStyle: "long" }).format(joined))
      : undefined;

  return (
    <div className="profile">
      <h1 className="profile__title">{t.me.title}</h1>
      {member.status === "error" || !data ? (
        <Notice>{t.home.notMember}</Notice>
      ) : (
        <MemberCard
          className="profile__card"
          displayName={data.member.nick || user.displayName}
          username={user.username}
          avatarUrl={user.avatarUrl || undefined}
          roles={data.member.roles}
          rolesLabel={t.me.roles}
          emptyRoles={t.onboarding.noRoles}
          caption={[data.role !== "member" ? t.me.access[data.role] : null, since]
            .filter(Boolean)
            .join(". ")}
        >
          <LevelCard />
          <div className="profile__actions">
            {pages.onboarding ? (
              <Link to="/onboarding" className="codi-button codi-button--primary codi-button--md">
                <span className="codi-button__label">{t.me.editRoles}</span>
              </Link>
            ) : null}
            <Button
              variant="ghost"
              onClick={() => {
                void logout().then(() => navigate("/"));
              }}
            >
              {t.logout}
            </Button>
          </div>
        </MemberCard>
      )}
    </div>
  );
}

/** The viewer's entry of the `levels` plugin (`GET /me`, member), when installed. */
function LevelCard() {
  const t = useT();
  const fmt = useNumber();
  const levels = usePlugin("levels", "GET /me");
  const mine = usePluginQuery<Partial<LeaderboardEntry> & { nextLevelXp?: number }>(
    levels ? { bot: levels.bot, instance: levels.instance } : undefined,
    "/me",
  );
  const d = mine.data;
  if (!d || typeof d.level !== "number") return null;
  const next = typeof d.nextLevelXp === "number" && d.nextLevelXp > 0 ? d.nextLevelXp : undefined;
  return (
    <div className="profile__level">
      <div className="profile__level-head">
        <strong>{t.me.level(d.level)}</strong>
        <span>
          {t.me.xp(fmt(d.xp ?? 0))}
          {typeof d.rank === "number" ? ` · ${t.me.rank(d.rank)}` : ""}
        </span>
      </div>
      {next ? (
        <ProgressBar value={Math.min(1, (d.xp ?? 0) / next)} label={t.me.level(d.level + 1)} />
      ) : null}
    </div>
  );
}
