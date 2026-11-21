/**
 * Contract of the `onboarding` plugin's portal routes
 * (examples/basic/plugins/onboarding — `GET /steps`, `POST /complete`).
 */
import type { PluginRoutes } from "./client.ts";

/** A role a member can pick, resolved by the plugin from the server's roles. */
export interface OnboardingOption {
  /** Role key (servers/<server>.yaml). Used in answers. */
  role: string;
  /** Label shown in the portal (config `label`, defaults to the role name). */
  label: string;
  emoji: string;
  description: string;
  /** Discord role name and color ("" when the role has none). */
  name: string;
  color: string;
}

export interface OnboardingStep {
  id: string;
  title: string;
  description: string;
  /** Several options may be selected. */
  multiple: boolean;
  /** At least one option must be selected. */
  required: boolean;
  options: OnboardingOption[];
  /** Role keys of this step the member currently has. */
  selected: string[];
}

export interface OnboardingRole {
  key: string;
  id: string;
  name: string;
  color: string;
}

/** `GET /steps` */
export interface OnboardingSteps {
  title: string;
  intro: string;
  steps: OnboardingStep[];
  /** Role given when the flow is completed, if configured. */
  completedRole: OnboardingRole | null;
  completed: boolean;
  completedAt: string | null;
}

/** Answers: step id → selected role keys. */
export type OnboardingAnswers = Record<string, string[]>;

/** `POST /complete` body. */
export interface OnboardingCompleteRequest {
  answers: OnboardingAnswers;
}

/** `POST /complete` response (200). Errors use the API error shape (`invalid`, 422/400). */
export interface OnboardingCompleteResponse {
  completed: true;
  completedAt: string;
  /** Member roles after the update. */
  roles: OnboardingRole[];
  /** Role keys added / removed by this call. */
  added: string[];
  removed: string[];
  /** Success message configured by the server. */
  message: string;
}

/** Typed wrapper over the onboarding plugin routes. */
export function onboardingApi(routes: PluginRoutes) {
  return {
    steps: (init?: { signal?: AbortSignal }) =>
      routes.get<OnboardingSteps>("/steps", undefined, init),
    complete: (answers: OnboardingAnswers, init?: { signal?: AbortSignal }) =>
      routes.post<OnboardingCompleteResponse>(
        "/complete",
        { answers } satisfies OnboardingCompleteRequest,
        init,
      ),
  };
}

export type OnboardingApi = ReturnType<typeof onboardingApi>;
