import type { PortalPlugin } from "@cicord/client";

type Access = "public" | "member" | "staff" | "scoped";
type Feature = {
  path: string;
  plugin: string;
  route: string;
  access: Access;
  title: [string, string];
  hidden?: boolean;
};
export const features = {
  cases: {
    path: "/moderation/cases",
    plugin: "moderation",
    route: "GET /cases",
    access: "staff",
    title: ["Moderation cases", "Dossiers de modération"],
  },
  history: {
    path: "/moderation/me",
    plugin: "moderation",
    route: "GET /members/:id",
    access: "member",
    title: ["My moderation history", "Mon historique de modération"],
  },
  member: {
    path: "/moderation/members/:id",
    plugin: "moderation",
    route: "GET /members/:id",
    access: "member",
    title: ["Member history", "Historique du membre"],
    hidden: true,
  },
  challenge: {
    path: "/verification/challenge",
    plugin: "verification",
    route: "GET /challenge",
    access: "member",
    title: ["Verify my account", "Vérifier mon compte"],
  },
  recovery: {
    path: "/verification/return/:id",
    plugin: "verification",
    route: "GET /return/:id",
    access: "scoped",
    title: ["Verification result", "Résultat de la vérification"],
    hidden: true,
  },
  queue: {
    path: "/verification/queue",
    plugin: "verification",
    route: "GET /queue",
    access: "staff",
    title: ["Verification queue", "Vérifications à examiner"],
  },
  announcements: {
    path: "/announcements",
    plugin: "announcements",
    route: "POST /compose",
    access: "staff",
    title: ["Announcements", "Annonces"],
  },
  birthday: {
    path: "/birthday",
    plugin: "birthdays",
    route: "GET /birthday",
    access: "member",
    title: ["My birthday", "Mon anniversaire"],
  },
  shifts: {
    path: "/staff-shifts",
    plugin: "staff-shifts",
    route: "GET /me",
    access: "member",
    title: ["Staff schedule", "Planning de l’équipe"],
  },
  suggestions: {
    path: "/suggestions",
    plugin: "suggestions",
    route: "GET /suggestions",
    access: "public",
    title: ["Suggestions", "Suggestions"],
  },
  proposals: {
    path: "/suggestions/moderation",
    plugin: "suggestions",
    route: "GET /suggestions/moderation",
    access: "staff",
    title: ["Review suggestions", "Examiner les suggestions"],
  },
  draws: {
    path: "/giveaways",
    plugin: "giveaways",
    route: "GET /giveaways",
    access: "staff",
    title: ["Manage giveaways", "Suivre les tirages"],
  },
  draw: {
    path: "/giveaways/:id",
    plugin: "giveaways",
    route: "GET /giveaways/:id",
    access: "public",
    title: ["Giveaway results", "Résultats du tirage"],
    hidden: true,
  },
  wallet: {
    path: "/economy",
    plugin: "economy",
    route: "GET /wallet",
    access: "member",
    title: ["My wallet", "Mon portefeuille"],
  },
  ledger: {
    path: "/economy/ledger",
    plugin: "economy",
    route: "GET /ledger",
    access: "staff",
    title: ["Transaction ledger", "Grand livre"],
  },
  tags: {
    path: "/tags",
    plugin: "tags",
    route: "GET /tags",
    access: "member",
    title: ["Tags", "Tags"],
  },
  questions: {
    path: "/ai-assistant/unanswered",
    plugin: "ai-assistant",
    route: "GET /unanswered",
    access: "staff",
    title: ["Unanswered questions", "Questions sans réponse"],
  },
  calendar: {
    path: "/calendar",
    plugin: "community-events",
    route: "GET /calendar",
    access: "public",
    title: ["Event calendar", "Calendrier des événements"],
  },
  invites: {
    path: "/invites",
    plugin: "invite-tracker",
    route: "GET /leaderboard",
    access: "member",
    title: ["Invitations", "Invitations"],
  },
  season: {
    path: "/levels/seasons/:id",
    plugin: "levels",
    route: "GET /seasons/:id",
    access: "public",
    title: ["Season standings", "Classement de saison"],
    hidden: true,
  },
  drawLookup: {
    path: "/giveaway-results",
    plugin: "giveaways",
    route: "GET /giveaways/:id",
    access: "public",
    title: ["Find a giveaway", "Retrouver un tirage"],
  },
  seasonLookup: {
    path: "/levels/seasons",
    plugin: "levels",
    route: "GET /seasons/:id",
    access: "public",
    title: ["Season standings", "Classements de saison"],
  },
  pollLookup: {
    path: "/poll-results",
    plugin: "polls",
    route: "GET /polls/:id",
    access: "public",
    title: ["Poll results", "Résultats de sondage"],
  },
  poll: {
    path: "/polls/:id",
    plugin: "polls",
    route: "GET /polls/:id",
    access: "public",
    title: ["Poll results", "Résultats du sondage"],
    hidden: true,
  },
  modStats: {
    path: "/moderation/statistics",
    plugin: "moderation",
    route: "GET /stats/moderators",
    access: "staff",
    title: ["Moderator statistics", "Statistiques par modérateur"],
  },
  violations: {
    path: "/guard/violations",
    plugin: "guard",
    route: "GET /violations",
    access: "staff",
    title: ["Recent violations", "Infractions récentes"],
  },
  raids: {
    path: "/guard/raids",
    plugin: "guard",
    route: "GET /raids",
    access: "staff",
    title: ["Raid history", "Historique des raids"],
  },
  blocklist: {
    path: "/guard/blocklist",
    plugin: "guard",
    route: "GET /blocklist",
    access: "staff",
    title: ["Guard blocklist", "Liste de blocage"],
  },
  incidents: {
    path: "/anti-nuke/incidents",
    plugin: "anti-nuke",
    route: "GET /incidents",
    access: "staff",
    title: ["Security incidents", "Incidents de sécurité"],
  },
  audit: {
    path: "/audit-log",
    plugin: "audit-log",
    route: "GET /search",
    access: "staff",
    title: ["Audit log", "Journal d’audit"],
  },
} satisfies Record<string, Feature>;
export type FeatureId = keyof typeof features;
export const entries = Object.entries(features) as [FeatureId, Feature][];
export function published(f: Feature, plugin: PortalPlugin) {
  return plugin.plugin === f.plugin && plugin.routes.includes(f.route);
}
export function visible(f: Feature, member: boolean, staff: boolean) {
  return (
    !f.hidden &&
    (f.access === "public" || (f.access === "member" && member) || (f.access === "staff" && staff))
  );
}
export function match(path: string) {
  for (const [id, feature] of entries) {
    if (feature.path === path) return { id, feature, param: "" };
    if (!feature.path.endsWith("/:id")) continue;
    const prefix = feature.path.slice(0, -3);
    if (!path.startsWith(prefix)) continue;
    const value = path.slice(prefix.length);
    if (/^[A-Za-z0-9_-]{1,128}$/.test(value)) return { id, feature, param: value };
  }
  return undefined;
}
export function featureUrl(path: string, server: string, p: PortalPlugin) {
  return path + "?" + new URLSearchParams({ server, bot: p.bot, instance: p.instance }).toString();
}
