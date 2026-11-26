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
