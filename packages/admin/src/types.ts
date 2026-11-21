/**
 * Fields the runtime returns beyond the @codi/client types (reported as client mismatches):
 * `lastError` on the status, a null revision before the first sync, an optional `lastSync`,
 * the reconciling `bot` of a server, `routes` on plugin instances, `key`/`ownerId` on
 * simulator servers and the modal of an interaction result.
 */
import type {
  InteractionResult,
  Json,
  PluginInstance,
  Revision,
  RuntimeStatus,
  ServerStatus,
  SimServer,
} from "@codi/client";

export type AdminServerStatus = Omit<ServerStatus, "lastSync"> & {
  lastSync?: string;
  bot?: string;
};

export type AdminStatus = Omit<RuntimeStatus, "revision" | "servers"> & {
  revision: (Revision & { sha?: string }) | null;
  lastError?: string;
  servers: AdminServerStatus[];
};

export type AdminPluginInstance = PluginInstance & {
  routes?: string[];
  /** `spec.plugins[].config` as written (no defaults). */
  baseConfig?: { [key: string]: Json };
  /** `overrides.<server>.config`, null when there is none. */
  override?: { [key: string]: Json } | null;
};

export type AdminSimServer = SimServer & { key?: string; ownerId?: string };

export interface SimModal {
  customId: string;
  title: string;
  components: { type: "row"; components: SimTextInput[] }[];
}

export interface SimTextInput {
  type: "textInput";
  customId: string;
  label: string;
  style?: "short" | "paragraph";
  required?: boolean;
  value?: string;
  placeholder?: string;
}

export type AdminInteractionResult = InteractionResult & { modal?: SimModal };
