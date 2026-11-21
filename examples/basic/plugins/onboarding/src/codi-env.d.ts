// Generated from config.schema.json (same output as `codi plugin typegen`) — do not edit.
// Nested fields with a default are optional: the runtime applies top-level defaults; the
// plugin falls back on nested ones itself.
declare module "@codi/sdk" {
  interface PluginConfig {
    title: string;
    intro: string;
    steps: {
      id: string;
      title: string;
      description?: string;
      multiple?: boolean;
      required?: boolean;
      options: {
        role: string;
        label?: string;
        emoji?: string;
        description?: string;
      }[];
    }[];
    completedRole?: string;
    successMessage: string;
    linkMessage: string;
    buttonLabel: string;
  }
}
export {};
