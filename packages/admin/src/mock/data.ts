/**
 * Mock data modelled on examples/basic (a server YAML, bots/moderator/bot.yaml and the two
 * example plugins), with English demo content: "Pixel Haven", a gaming community. Used by
 * `vp dev --mode mock` (product screenshots) and the tests.
 */
import type { Json } from "@cicord/client";
import onboardingSchema from "../../../../examples/basic/plugins/onboarding/config.schema.json";
import welcomeSchema from "../../../../examples/basic/plugins/welcome/config.schema.json";
import levelsSchema from "../../../../examples/showcase/plugins/levels/config.schema.json";
import moderationSchema from "../../../../examples/showcase/plugins/moderation/config.schema.json";
import pollsSchema from "../../../../examples/showcase/plugins/polls/config.schema.json";
import ticketsSchema from "../../../../examples/showcase/plugins/tickets/config.schema.json";
import type { JsonSchema } from "../schema-form/schema.ts";

export const SERVER_KEY = "pixel-haven";
export const SERVER_NAME = "Pixel Haven";
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
  { key: "mod", id: "r-mod", name: "Moderator", color: "#e67e22" },
  { key: "member", id: "r-member", name: "Member", color: "#3498db" },
  { key: "minecraft", id: "r-mc", name: "Minecraft", color: "#5b8731" },
  { key: "valorant", id: "r-valo", name: "Valorant", color: "#ff4655" },
  { key: "lol", id: "r-lol", name: "League of Legends", color: "#c89b3c" },
  { key: "pc", id: "r-pc", name: "PC", color: "#95a5a6" },
  { key: "console", id: "r-console", name: "Console", color: "#9b59b6" },
  { key: "mobile", id: "r-mobile", name: "Mobile", color: "#1abc9c" },
  { key: "ping-events", id: "r-ping", name: "Event Pings", color: "#f1c40f" },
];

/** Roles that exist on Discord but are not declared in YAML. */
export const unmanagedRoles: MockRole[] = [
  { key: "", id: "r-warden", name: "Warden", color: "#7289da" },
  { key: "", id: "r-vip", name: "VIP 2023", color: "#ad1457" },
];

export const categories = [
  { key: "info", id: "c-info", name: "Info" },
  { key: "community", id: "c-community", name: "Community" },
  { key: "staff", id: "c-staff", name: "Staff" },
];

export const channels = [
  { key: "rules", id: "ch-rules", name: "rules", type: "text", category: "info" },
  {
    key: "announcements",
    id: "ch-announcements",
    name: "announcements",
    type: "announcement",
    category: "info",
  },
  { key: "general", id: "ch-general", name: "general", type: "text", category: "community" },
  { key: "voice", id: "ch-voice", name: "Lounge", type: "voice", category: "community" },
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
  mina: { id: "104", username: "mina", displayName: "Mina (mod)", avatarUrl: "" },
};

export const bot = { id: "900", username: "Warden", displayName: "Warden", avatarUrl: "" };

export const welcomeBase: { [key: string]: Json } = {
  channel: "general",
  message: "Welcome {member} to {server}! 👋",
  autoRole: "member",
  dmOnboarding: true,
};

export const welcomeOverride: { [key: string]: Json } = {
  message: "Hey {member}, welcome to {server}! Take a minute to read the rules.",
};

export const onboardingBase: { [key: string]: Json } = {
  title: "Welcome to Pixel Haven!",
  intro:
    "Three quick questions to set things up for you. You can change your answers anytime with /onboarding.",
  steps: [
    {
      id: "games",
      title: "What do you play?",
      description: "You get the channels and pings for your games.",
      multiple: true,
      options: [
        { role: "minecraft", emoji: "⛏️", description: "Survival, creative and the community SMP" },
        { role: "valorant", emoji: "🎯", description: "Ranked, customs and tournaments" },
        {
          role: "lol",
          label: "League of Legends",
          emoji: "🛡️",
          description: "Flex, ARAM and Clash",
        },
      ],
    },
    {
      id: "platform",
      title: "Where do you mostly play?",
      description: "So you can find teammates on the same platform.",
      required: true,
      options: [
        { role: "pc", emoji: "🖥️" },
        { role: "console", emoji: "🎮" },
        { role: "mobile", emoji: "📱" },
      ],
    },
    {
      id: "notifications",
      title: "Want a heads-up for events?",
      description: "Game nights, tournaments and community streams. No spam, promise.",
      options: [
        {
          role: "ping-events",
          label: "Yes, ping me!",
          emoji: "🔔",
          description: "You get mentioned for every new event",
        },
      ],
    },
  ],
  completedRole: "member",
  successMessage: "All set! Your roles are updated, come say hi in #general.",
  linkMessage: "Pick your roles on the community portal:",
  buttonLabel: "Pick my roles",
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
  profile: { name: SERVER_NAME, verificationLevel: "medium", systemChannel: "general" },
  syncPolicy: { selfHeal: false, prune: false, deletionProtection: true },
  access: { admins: { roles: ["admin"] }, moderators: { roles: ["mod"] } },
};

// ───────────────────────────────────────────────────────────── store (https://cicord.dev/docs/reference/http-api/store/)

/** A plugin of a mock Store source, with what its plugin.yaml requests. */
export interface MockStorePlugin {
  name: string;
  displayName: string;
  description: string;
  keywords: string[];
  version: string;
  publisher?: string;
  verified: boolean;
  repo?: string;
  capabilities: string[];
  intents: string[];
  network: string[];
  schema: JsonSchema;
  /** null: unknown before install (unofficial sources). */
  commands: { name: string; description: string }[] | null;
}

/** Privileged intents (they must be enabled in the Discord developer portal). */
export const PRIVILEGED_INTENTS = ["ServerMembers", "MessageContent", "ServerPresences"];
/** Privileged intents enabled on the mock bot's application. */
export const ENABLED_INTENTS = ["ServerMembers"];

/** The official plugins bundled in the binary (the catalog, source `cicord`). */
export const bundledPlugins: MockStorePlugin[] = [
  {
    ...plugins.welcome,
    keywords: ["welcome", "onboarding"],
    verified: true,
    publisher: "cicord",
    network: [],
    schema: schemas["welcome"]!,
  },
  {
    ...plugins.onboarding,
    keywords: ["onboarding", "roles", "portal"],
    verified: true,
    publisher: "cicord",
    network: [],
    schema: schemas["onboarding"]!,
  },
  {
    name: "tickets",
    version: "0.2.1",
    displayName: "Tickets",
    description:
      "Support tickets in private channels, opened from a panel button or the community portal. Members follow theirs on the portal; staff manage the queue there (assignee, priority, labels, replies) and read ticket channels (MessageContent). Closing posts a summary to the staff log.",
    keywords: ["tickets", "support", "helpdesk", "portal"],
    verified: true,
    publisher: "cicord",
    capabilities: [
      "discord.messages.send",
      "discord.messages.read",
      "discord.channels.read",
      "discord.channels.manage",
      "discord.members.read",
      "storage.kv",
      "portal.routes",
    ],
    intents: ["ServerMessages", "MessageContent", "ServerMembers"],
    network: [],
    schema: ticketsSchema as unknown as JsonSchema,
    commands: [{ name: "ticket", description: "Open a support ticket" }],
  },
  {
    name: "polls",
    version: "0.1.0",
    displayName: "Polls",
    description:
      "Quick polls with buttons or a select menu, live results, automatic closing and an optional discussion thread.",
    keywords: ["polls", "votes", "community", "events"],
    verified: true,
    publisher: "cicord",
    capabilities: [
      "discord.messages.send",
      "discord.messages.read",
      "discord.channels.read",
      "discord.threads.manage",
      "storage.kv",
      "scheduler",
      "events.publish",
      "portal.routes",
    ],
    intents: [],
    network: [],
    schema: pollsSchema as unknown as JsonSchema,
    commands: [{ name: "poll", description: "Start a poll" }],
  },
  {
    name: "levels",
    version: "0.1.0",
    displayName: "Levels",
    description:
      "XP and levels for chatting, receiving reactions and voting in polls. Level-up announcements, role rewards, /rank, /top and a public leaderboard.",
    keywords: ["xp", "levels", "leaderboard", "roles", "portal"],
    verified: true,
    publisher: "cicord",
    capabilities: [
      "discord.messages.send",
      "discord.reactions.manage",
      "discord.channels.read",
      "discord.members.read",
      "discord.roles.assign",
      "storage.kv",
      "scheduler",
      "events.subscribe",
      "portal.routes",
      "portal.links",
    ],
    intents: ["ServerMessages", "MessageContent", "ServerMessageReactions"],
    network: [],
    schema: levelsSchema as unknown as JsonSchema,
    commands: [
      { name: "rank", description: "Your level and XP" },
      { name: "top", description: "The leaderboard" },
    ],
  },
  {
    name: "moderation",
    version: "0.1.0",
    displayName: "Moderation",
    description:
      "/warn, /timeout, /kick and /ban with an audit log, expiring warnings, automatic escalation, flood protection and an optional webhook.",
    keywords: ["moderation", "warnings", "audit", "automod"],
    verified: true,
    publisher: "cicord",
    capabilities: [
      "discord.messages.send",
      "discord.messages.manage",
      "discord.channels.read",
      "discord.members.read",
      "discord.members.moderate",
      "discord.dm.send",
      "storage.kv",
      "http.fetch",
      "portal.routes",
    ],
    intents: ["ServerMembers", "ServerMessages"],
    network: ["hooks.slack.com", "discord.com"],
    schema: moderationSchema as unknown as JsonSchema,
    commands: [
      { name: "warn", description: "Warn a member" },
      { name: "timeout", description: "Time out a member" },
      { name: "kick", description: "Kick a member" },
      { name: "ban", description: "Ban a member" },
    ],
  },
];

/** Registries of the mock cicord.yaml (spec.registries). */
export interface MockRegistry {
  name: string;
  url: string;
  /** Index fetch failure (the registry is listed with an error and no plugins). */
  error?: { code: string; message: string; hint?: string; details?: string };
  /** Private registry: the secret holding its access token (cicord.yaml `secretRef`). */
  secretRef?: string;
  plugins: (MockStorePlugin & { commit: string; integrity: string })[];
}

export const seedRegistries = (): MockRegistry[] => [
  {
    name: "acme",
    url: "https://github.com/acme/cicord-plugins.git",
    plugins: [
      {
        name: "xp",
        displayName: "XP & levels",
        description: "Levels and a portal leaderboard, with weekly XP reports sent to acme.dev.",
        keywords: ["xp", "levels"],
        version: "1.2.0",
        publisher: "acme",
        verified: false,
        repo: "https://github.com/acme/cicord-xp",
        capabilities: [
          "discord.messages.read",
          "discord.messages.send",
          "storage.kv",
          "http.fetch",
        ],
        intents: ["ServerMessages", "MessageContent"],
        network: ["api.acme.dev"],
        schema: {
          type: "object",
          properties: {
            announceChannel: {
              type: "string",
              title: "Announcement channel",
              description: "Where level-ups are announced.",
              "x-cicord-ref": "channel",
            },
            xpPerMessage: { type: "integer", title: "XP per message", default: 10, minimum: 1 },
          },
          required: ["announceChannel"],
        } as unknown as JsonSchema,
        commands: null,
        commit: "3c9a1f04e2b7d8a6c5e4f3a2b1c0d9e8f7a6b5c4",
        integrity: "sha256-q5Qm3n9xJb3k1vA8bG2sYwP0e7tZr4uLcHf6dXoN1iE=",
      },
      {
        name: "giveaways",
        displayName: "Giveaways",
        description: "Run giveaways with a button to enter and a random draw at the end.",
        keywords: ["giveaways", "events", "community"],
        version: "0.4.1",
        publisher: "acme",
        verified: false,
        repo: "https://github.com/acme/cicord-giveaways",
        capabilities: [
          "discord.messages.send",
          "discord.reactions.manage",
          "storage.kv",
          "scheduler",
        ],
        intents: [],
        network: [],
        schema: {
          type: "object",
          properties: {
            channel: {
              type: "string",
              title: "Giveaway channel",
              "x-cicord-ref": "channel",
              default: "general",
            },
            winners: { type: "integer", title: "Winners per giveaway", default: 1, minimum: 1 },
          },
        } as unknown as JsonSchema,
        commands: null,
        commit: "8e1d2c3b4a5f60718293a4b5c6d7e8f9a0b1c2d3",
        integrity: "sha256-Zk3pQ8wR1vT6yU2iO9aS5dF4gH7jK0lZxC3vB6nM8qE=",
      },
    ],
  },
  {
    name: "community",
    url: "https://git.example.org/community/cicord-plugins.git",
    // Private, but the token is not in this runtime's secret store.
    secretRef: "CICORD_REGISTRY_COMMUNITY_TOKEN",
    error: {
      code: "fetch_failed",
      message: "Could not fetch the registry index",
      hint: "Check the URL and that the repository is public (or that Cicord's git token can read it), then refresh the sources.",
      details:
        "git ls-remote https://git.example.org/community/cicord-plugins.git: fatal: repository not found",
    },
    plugins: [],
  },
  {
    name: "hub",
    url: "https://git.cicord-hub.dev/hub/cicord-plugins.git",
    secretRef: "CICORD_REGISTRY_HUB_TOKEN",
    plugins: hubPlugins(),
  },
];

// ───────────────────────────────────────────────────────────── generated registry

const HUB_TOPICS = [
  ["birthday", "Birthdays", "Wishes members a happy birthday and gives them a role for the day."],
  ["counter", "Counting game", "A channel where members count together; a wrong number resets it."],
  ["starboard", "Starboard", "Reposts messages that collect enough stars in a highlights channel."],
  ["reminders", "Reminders", "Personal and channel reminders with natural dates."],
  ["autorole", "Auto roles", "Gives roles from reactions, buttons or activity."],
  ["logs", "Server logs", "Logs joins, leaves, edits and deletions to a staff channel."],
  [
    "music-queue",
    "Music queue",
    "A shared queue of tracks for the voice channel, with votes to skip.",
  ],
  ["trivia", "Trivia", "Quiz rounds with a scoreboard and weekly winners."],
  ["suggestions", "Suggestions", "Members post ideas, the community votes, staff marks them done."],
  [
    "welcome-cards",
    "Welcome cards",
    "Image cards for new members with their avatar and member count.",
  ],
  [
    "events",
    "Event planner",
    "Plan game nights with RSVP buttons and reminders before they start.",
  ],
  ["translate", "Translate", "Translates messages on demand with a context menu command."],
] as const;
const HUB_PUBLISHERS = ["hub", "lumen", "nordic-bots", "pixel", "acme"];

/** ~120 plugins to exercise the Store table (pagination, sorting, search). */
function hubPlugins(): MockRegistry["plugins"] {
  const out: MockRegistry["plugins"] = [];
  for (let i = 0; i < 120; i++) {
    const [base, title, description] = HUB_TOPICS[i % HUB_TOPICS.length]!;
    const n = Math.floor(i / HUB_TOPICS.length);
    const name = n ? `${base}-${n + 1}` : base;
    const hex = (i * 2654435761).toString(16).padStart(8, "0").slice(-8);
    out.push({
      name,
      displayName: n ? `${title} ${n + 1}` : title,
      description,
      keywords: [base.split("-")[0]!, "community"],
      version: `${(i % 3) + (n % 2)}.${(i * 7) % 12}.${i % 5}`,
      publisher: HUB_PUBLISHERS[i % HUB_PUBLISHERS.length]!,
      verified: false,
      repo: `https://git.cicord-hub.dev/hub/${name}`,
      capabilities: ["discord.messages.send", "discord.channels.read", "storage.kv"].concat(
        i % 4 === 0 ? ["discord.roles.assign"] : [],
      ),
      intents: i % 5 === 0 ? ["ServerMessages"] : [],
      network: i % 9 === 0 ? ["api.cicord-hub.dev"] : [],
      schema: {
        type: "object",
        properties: {
          channel: {
            type: "string",
            title: "Channel",
            "x-cicord-ref": "channel",
            default: "general",
          },
          enabled: {
            type: "boolean",
            title: "Enabled",
            default: true,
            "x-cicord-access": "moderator",
          },
        },
      } as unknown as JsonSchema,
      commands: null,
      commit: hex.repeat(5),
      integrity: `sha256-${hex}${"A".repeat(35)}=`,
    });
  }
  return out;
}
