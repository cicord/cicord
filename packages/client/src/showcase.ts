/**
 * Contracts of the showcase plugins' portal routes (examples/showcase): `tickets` and
 * `levels`. They illustrate typing a plugin's routes on top of `client.plugin(...)`; extra
 * fields sent by a plugin are kept as is.
 */
import type { CallOptions, PluginRoutes } from "./client.ts";

export type TicketStatus = "open" | "closed";

export interface Ticket {
  /** Ticket number, as a string. */
  id: string;
  subject: string;
  status: TicketStatus;
  author: { id: string; displayName: string };
  createdAt: string;
}

/** tickets `GET /tickets` (moderator), newest first. */
export interface TicketList {
  items: Ticket[];
}

export interface LeaderboardEntry {
  /** From 1. */
  rank: number;
  userId: string;
  displayName: string;
  avatarUrl: string | null;
  level: number;
  xp: number;
}

/** levels `GET /leaderboard` (public). */
export interface Leaderboard {
  items: LeaderboardEntry[];
}

/** Typed wrapper over the `tickets` plugin routes. */
export function ticketsApi(routes: PluginRoutes) {
  return {
    list: (query: { status?: TicketStatus } = {}, options?: CallOptions) =>
      routes.get<TicketList>("/tickets", query, options),
  };
}

/** Typed wrapper over the `levels` plugin routes. */
export function levelsApi(routes: PluginRoutes) {
  return {
    leaderboard: (query: { limit?: number } = {}, options?: CallOptions) =>
      routes.get<Leaderboard>("/leaderboard", query, options),
    /** levels `GET /me` (member): the viewer's own entry (shape owned by the plugin). */
    me: <T = LeaderboardEntry>(options?: CallOptions) => routes.get<T>("/me", undefined, options),
  };
}
