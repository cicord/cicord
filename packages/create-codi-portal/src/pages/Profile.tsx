import { useMe, useMember, useLogout } from "@codi/react";
import { Button, MemberCard, Notice, Spinner } from "@codi/ui";
import { t } from "../i18n/fr.ts";
import { useLogin, useServer } from "../portal.tsx";
import { Link, navigate } from "../router.tsx";

export function Profile() {
  const server = useServer();
  const me = useMe();
  const member = useMember();
  const login = useLogin();
  const logout = useLogout();

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
        <Button size="lg" onClick={login}>
          {t.login}
        </Button>
      </div>
    );
  }
  const data = member.data;
  const joined = data?.member.joinedAt ? new Date(data.member.joinedAt) : undefined;
  const since =
    joined && !Number.isNaN(joined.getTime())
      ? t.me.since(new Intl.DateTimeFormat(t.locale, { dateStyle: "long" }).format(joined))
      : undefined;
  const hasOnboarding = server.plugins.some((p) => p.plugin === "onboarding");

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
          <div className="profile__actions">
            {hasOnboarding ? (
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
