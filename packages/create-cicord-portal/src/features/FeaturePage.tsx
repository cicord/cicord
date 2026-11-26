import { useCicord, useMe, useMember } from "@cicord/react";
import { Notice, Spinner } from "@cicord/ui";
import { useContext, useMemo, useSyncExternalStore } from "react";
import { useT } from "../i18n/index.tsx";
import { PortalLinkStatus, SignIn, useServer } from "../portal.tsx";
import { Link } from "../router.tsx";
import { NotFound } from "../pages/NotFound.tsx";
import { entries, featureUrl, match, published, visible, type FeatureId } from "./catalog.ts";
import { Announcements } from "./Announcements.tsx";
import { Birthdays } from "./Birthday.tsx";
import { Shifts } from "./Shifts.tsx";
import { Cases, MemberCases } from "./Moderation.tsx";
import { Recovery, Queue, Verification } from "./Verification.tsx";
import { Scope, useCopy, useScope } from "./shared.tsx";
import { Draw, Draws, Ledger, Lookup, Season, Suggestions, Wallet } from "./Rewards.tsx";
import { Tags, Unanswered } from "./Knowledge.tsx";
import { Calendar, Invites, Poll } from "./Events.tsx";
import { Incidents, ModeratorStats, Violations, Raids, Blocklist } from "./Security.tsx";
import { Audit } from "./Audit.tsx";
import "./features.css";

function subscribe(listener: () => void) {
  window.addEventListener("popstate", listener);
  window.addEventListener("cicord:navigate", listener);
  return () => {
    window.removeEventListener("popstate", listener);
    window.removeEventListener("cicord:navigate", listener);
  };
}
function Screen({ id, param }: { id: FeatureId; param: string }) {
  const scope = useScope();
  switch (id) {
    case "cases":
      return <Cases />;
    case "history":
      return <MemberCases id={scope.viewer} />;
    case "member":
      return <MemberCases id={param} />;
    case "challenge":
      return <Verification />;
    case "recovery":
      return <Recovery id={param} />;
    case "queue":
      return <Queue />;
    case "announcements":
      return <Announcements />;
    case "birthday":
      return <Birthdays />;
    case "shifts":
      return <Shifts />;
    case "suggestions":
      return <Suggestions moderation={false} />;
    case "proposals":
      return <Suggestions moderation />;
    case "draws":
      return <Draws />;
    case "draw":
      return <Draw id={param} />;
    case "drawLookup":
      return <Lookup kind="giveaway" />;
    case "seasonLookup":
      return <Lookup kind="season" />;
    case "season":
      return <Season id={param} />;
    case "wallet":
      return <Wallet />;
    case "ledger":
      return <Ledger staff />;
    case "tags":
      return <Tags />;
    case "questions":
      return <Unanswered />;
    case "calendar":
      return <Calendar />;
    case "invites":
      return <Invites />;
    case "pollLookup":
      return <Lookup kind="poll" />;
    case "poll":
      return <Poll id={param} />;
    case "violations":
      return <Violations />;
    case "raids":
      return <Raids />;
    case "blocklist":
      return <Blocklist />;
    case "incidents":
      return <Incidents />;
    case "audit":
      return <Audit />;
    case "modStats":
      return <ModeratorStats />;
    default:
      return <NotFound />;
  }
}
export function FeaturePage({ path }: { path: string }) {
  const c = useCopy(),
    t = useT(),
    server = useServer(),
    { client } = useCicord(),
    me = useMe(),
    member = useMember();
  const exchanging = useContext(PortalLinkStatus);
  const search = useSyncExternalStore(
    subscribe,
    () => window.location.search,
    () => "",
  );
  const query = new URLSearchParams(search),
    viewer = me.data?.user.id ?? "";
  const role = viewer && member.data?.user.id === viewer ? member.data.role : undefined,
    staff = role === "admin" || role === "moderator";
  const route = match(path);
  const installed = route ? server.plugins.filter((p) => published(route.feature, p)) : [];
  const bot = query.get("bot"),
    instance = query.get("instance");
  const plugin =
    bot || instance
      ? installed.find((p) => p.bot === bot && p.instance === instance)
      : installed[0];
  const api = useMemo(
    () => (plugin ? client.plugin(server.key, plugin.bot, plugin.instance) : undefined),
    [client, plugin, server.key],
  );
  if (query.get("server") && query.get("server") !== server.key) return <NotFound />;
  if (path === "/community") {
    const groups = [
      { name: c("Community", "Communauté"), access: "public" },
      { name: c("For members", "Pour les membres"), access: "member" },
      { name: c("For staff", "Pour l’équipe"), access: "staff" },
    ];
    const available = entries.filter(
      ([, f]) => visible(f, !!role, staff) && server.plugins.some((p) => published(f, p)),
    );
    if (!available.length) {
      if (me.isLoading || member.isLoading) return <Spinner label={t.loading} />;
      return <NotFound />;
    }
    return (
      <section className="page feature-page">
        <header className="page__header">
          <h1>{c("Community tools", "Outils communautaires")}</h1>
        </header>
        {groups.map((g) => {
          const pages = available.filter(([, f]) => f.access === g.access);
          return pages.length ? (
            <section key={g.access} className="feature-group">
              <h2>{g.name}</h2>
              <ul className="feature-directory">
                {pages.flatMap(([id, f]) => {
                  const providers = server.plugins.filter((p) => published(f, p));
                  return providers.map((p) => (
                    <li key={id + "/" + p.bot + "/" + p.instance}>
                      <Link to={featureUrl(f.path, server.key, p)}>
                        {c(...f.title)}
                        {providers.length > 1 ? (
                          <small>
                            {p.bot} / {p.instance}
                          </small>
                        ) : null}
                      </Link>
                    </li>
                  ));
                })}
              </ul>
            </section>
          ) : null;
        })}
      </section>
    );
  }
  if (!route || !plugin || !api) return <NotFound />;
  const { feature } = route;
  const heading = (
    <header className="page__header">
      <h1>{c(...feature.title)}</h1>
    </header>
  );
  if (exchanging === "pending" || (me.isLoading && feature.access !== "public"))
    return <Spinner label={t.loading} />;
  if (exchanging === "error" && feature.access !== "public")
    return (
      <div className="page">
        {heading}
        <Notice tone="error">
          {c("This link is invalid or has expired.", "Ce lien est invalide ou a expiré.")}
        </Notice>
      </div>
    );
  if (feature.access !== "public" && !viewer)
    return (
      <section className="page">
        {heading}
        <SignIn />
      </section>
    );
  if (feature.access === "member" || feature.access === "staff") {
    if (member.isLoading || member.isFetching) return <Spinner label={t.loading} />;
    if (!role || (feature.access === "staff" && !staff))
      return (
        <section className="page">
          {heading}
          <Notice>
            {c("You do not have access to this page.", "Vous n’avez pas accès à cette page.")}
          </Notice>
        </section>
      );
  }
  if (route.id === "member" && !staff && viewer !== route.param) return <NotFound />;
  return (
    <Scope.Provider value={{ api, plugin, viewer, staff }}>
      <section
        className="page feature-page"
        key={JSON.stringify([
          server.key,
          plugin.bot,
          plugin.instance,
          viewer,
          role,
          route.id,
          route.param,
        ])}
      >
        {feature.access !== "scoped" ? (
          <Link
            className="feature-back"
            to={"/community?" + new URLSearchParams({ server: server.key })}
          >
            {c("Community tools", "Outils communautaires")}
          </Link>
        ) : null}
        {heading}
        <Screen id={route.id} param={route.param} />
      </section>
    </Scope.Provider>
  );
}
