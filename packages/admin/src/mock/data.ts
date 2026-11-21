/**
 * Mock data mirroring examples/basic (servers/ma-commu.yaml, bots/moderator/bot.yaml and the
 * two example plugins). Used by `vp dev --mode mock` and the tests.
 */
import type { Json } from "@codi/client";
import onboardingSchema from "../../../../examples/basic/plugins/onboarding/config.schema.json";
import welcomeSchema from "../../../../examples/basic/plugins/welcome/config.schema.json";
import type { JsonSchema } from "../schema-form/schema.ts";

export const SERVER_KEY = "ma-commu";
export const SERVER_ID = "112233445566778899";

export const schemas: Record<string, JsonSchema> = {
  welcome: welcomeSchema as unknown as JsonSchema,
  onboarding: onboardingSchema as unknown as JsonSchema,
};

export interface MockRole {
  key: string;
  id: string;
  name: string;
  color: string;
}

export const roles: MockRole[] = [
  { key: "admin", id: "r-admin", name: "Admin", color: "#e74c3c" },
  { key: "modo", id: "r-modo", name: "Modo", color: "#e67e22" },
  { key: "member", id: "r-member", name: "Membre", color: "#3498db" },
  { key: "minecraft", id: "r-mc", name: "Minecraft", color: "#5b8731" },
  { key: "valorant", id: "r-valo", name: "Valorant", color: "#ff4655" },
  { key: "lol", id: "r-lol", name: "League of Legends", color: "#c89b3c" },
  { key: "pc", id: "r-pc", name: "PC", color: "#95a5a6" },
  { key: "console", id: "r-console", name: "Console", color: "#9b59b6" },
  { key: "mobile", id: "r-mobile", name: "Mobile", color: "#1abc9c" },
  { key: "ping-events", id: "r-ping", name: "Notifs événements", color: "#f1c40f" },
];

/** Roles that exist on Discord but are not declared in YAML. */
export const unmanagedRoles: MockRole[] = [
  { key: "", id: "r-gardien", name: "Gardien", color: "#7289da" },
  { key: "", id: "r-vip", name: "VIP 2023", color: "#ad1457" },
];

export const categories = [
  { key: "info", id: "c-info", name: "Infos" },
  { key: "community", id: "c-community", name: "Communauté" },
  { key: "staff", id: "c-staff", name: "Staff" },
];

export const channels = [
  { key: "rules", id: "ch-rules", name: "règlement", type: "text", category: "info" },
  {
    key: "announcements",
    id: "ch-announcements",
    name: "annonces",
    type: "announcement",
    category: "info",
  },
  { key: "general", id: "ch-general", name: "général", type: "text", category: "community" },
  { key: "voice", id: "ch-voice", name: "Vocal", type: "voice", category: "community" },
  { key: "staff-chat", id: "ch-staff", name: "staff", type: "text", category: "staff" },
];

export const unmanagedChannels = [
  { key: "", id: "ch-memes", name: "memes", type: "text", category: "community" },
];

export const users = {
  owner: { id: "100", username: "owner", displayName: "Owner", avatarUrl: "" },
  alice: { id: "101", username: "alice", displayName: "Alice", avatarUrl: "" },
  bob: { id: "102", username: "bob", displayName: "Bob", avatarUrl: "" },
  charlie: { id: "103", username: "charlie", displayName: "Charlie", avatarUrl: "" },
  mina: { id: "104", username: "mina", displayName: "Mina (modo)", avatarUrl: "" },
};

export const bot = { id: "900", username: "Gardien", displayName: "Gardien", avatarUrl: "" };

export const welcomeBase: { [key: string]: Json } = {
  channel: "general",
  message: "Bienvenue {member} sur {server} ! 👋",
  autoRole: "member",
  dmOnboarding: true,
};

export const welcomeOverride: { [key: string]: Json } = {
  message: "Salut {member}, bienvenue sur {server} ! Passe lire le règlement.",
};

export const onboardingBase: { [key: string]: Json } = {
  title: "Bienvenue sur Ma Commu !",
  intro:
    "Trois petites questions pour personnaliser ton expérience. Tu pourras changer d'avis quand tu veux avec /onboarding.",
  steps: [
    {
      id: "jeux",
      title: "À quoi tu joues ?",
      description: "Tu auras accès aux salons et aux pings de tes jeux.",
      multiple: true,
      options: [
        { role: "minecraft", emoji: "⛏️", description: "Survie, créatif et serveur de la commu" },
        { role: "valorant", emoji: "🎯", description: "Ranked, customs et tournois" },
        {
          role: "lol",
          label: "League of Legends",
          emoji: "🛡️",
          description: "Flex, ARAM et Clash",
        },
      ],
    },
    {
      id: "plateforme",
      title: "Tu joues surtout sur…",
      description: "Pour trouver des coéquipiers sur la même plateforme.",
      required: true,
      options: [
        { role: "pc", emoji: "🖥️" },
        { role: "console", emoji: "🎮" },
        { role: "mobile", emoji: "📱" },
      ],
    },
    {
      id: "notifs",
      title: "On te prévient pour les événements ?",
      description: "Soirées jeux, tournois et lives de la commu. Zéro spam, promis.",
      options: [
        {
          role: "ping-events",
          label: "Oui, ping-moi !",
          emoji: "🔔",
          description: "Tu seras mentionné à chaque nouvel événement",
        },
      ],
    },
  ],
  completedRole: "member",
  successMessage: "C'est tout bon ! Tes rôles sont à jour, file dire bonjour dans #général.",
  linkMessage: "Choisis tes rôles sur le portail de la commu :",
  buttonLabel: "Choisir mes rôles",
};

export const plugins = {
  welcome: {
    name: "welcome",
    version: "0.1.0",
    displayName: "Welcome",
    description: "Greets new members, gives them a default role and sends an onboarding link.",
    capabilities: [
      "discord.messages.send",
      "discord.channels.read",
      "discord.members.read",
      "discord.roles.assign",
      "discord.dm.send",
      "portal.links",
      "storage.kv",
    ],
    intents: ["ServerMembers"],
    commands: [
      { name: "welcome-preview", description: "Preview the welcome message" },
      { name: "welcome-stats", description: "How many members were welcomed" },
    ],
  },
  onboarding: {
    name: "onboarding",
    version: "0.1.0",
    displayName: "Onboarding",
    description:
      "Step-by-step role picking on the community portal (games, platforms, notifications…), with a /onboarding command that links to it.",
    capabilities: ["discord.roles.assign", "storage.kv", "portal.routes", "portal.links"],
    intents: [],
    commands: [{ name: "onboarding", description: "Pick your roles on the community portal" }],
  },
};

export const serverSpec: { [key: string]: Json } = {
  discordId: SERVER_ID,
  profile: { name: "Ma Commu", verificationLevel: "medium", systemChannel: "general" },
  syncPolicy: { selfHeal: false, prune: false, deletionProtection: true },
  access: { admins: { roles: ["admin"] }, moderators: { roles: ["modo"] } },
};
