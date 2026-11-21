import { useLinkTokenLogin, useMe, useMember, usePluginQuery } from "@codi/react";
import type { OnboardingSteps } from "@codi/client";
import { Button, Notice, Spinner } from "@codi/ui";
import { t } from "../i18n/fr.ts";
import { ServerMark, useLogin, useServer } from "../portal.tsx";
import { Link } from "../router.tsx";

export function Home() {
  const server = useServer();
  const me = useMe();
  const linkLogin = useLinkTokenLogin();
  const login = useLogin();
  const user = me.data?.user;

  return (
    <div className="home">
      <section className="home-hero" aria-labelledby="home-title">
        <ServerMark size={112} className="home-hero__mark" />
        <h1 id="home-title" className="home-hero__title">
          {server.name}
        </h1>
        <p className="home-hero__lead">{t.portalOf(server.name)}</p>
      </section>

      <section className="home-panel" aria-live="polite">
        {me.isLoading || linkLogin.status === "pending" ? (
          <div className="home-panel__loading">
            <Spinner label={t.loading} />
          </div>
        ) : user ? (
          <MemberHome name={user.displayName} />
        ) : (
          <div className="home-login">
            <h2>{t.home.loggedOutTitle}</h2>
            <p>{t.home.loggedOut}</p>
            <Button size="lg" onClick={login}>
              {t.login}
            </Button>
          </div>
        )}
      </section>
    </div>
  );
}

function MemberHome({ name }: { name: string }) {
  const server = useServer();
  const member = useMember();
  const onboarding = server.plugins.find((p) => p.plugin === "onboarding");
  const steps = usePluginQuery<OnboardingSteps>(
    onboarding && member.data ? { bot: onboarding.bot, instance: onboarding.instance } : undefined,
    "/steps",
  );

  if (member.status === "error") {
    return (
      <Notice tone="info" title={t.home.hello(name)}>
        {t.home.notMember}
      </Notice>
    );
  }

  return (
    <div className="home-member">
      <h2>{t.home.hello(name)}</h2>
      <ul className="home-actions">
        {onboarding ? (
          <li>
            <Link to="/onboarding" className="home-action">
              <span className="home-action__icon" aria-hidden="true">
                ✨
              </span>
              <span className="home-action__text">
                <span className="home-action__title">{t.home.onboardingTitle}</span>
                <span className="home-action__desc">
                  {steps.data?.completed ? t.home.onboardingDone : t.home.onboardingTodo}
                </span>
              </span>
              <Chevron />
            </Link>
          </li>
        ) : null}
        <li>
          <Link to="/me" className="home-action">
            <span className="home-action__icon" aria-hidden="true">
              🪪
            </span>
            <span className="home-action__text">
              <span className="home-action__title">{t.home.profileTitle}</span>
              <span className="home-action__desc">{t.home.profileText}</span>
            </span>
            <Chevron />
          </Link>
        </li>
      </ul>
    </div>
  );
}

function Chevron() {
  return (
    <svg
      className="home-action__chevron"
      viewBox="0 0 16 16"
      width="16"
      height="16"
      aria-hidden="true"
    >
      <path
        d="M6 3.5L10.5 8 6 12.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
