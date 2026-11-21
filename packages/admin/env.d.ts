/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Origin of the CODI runtime when the admin is served elsewhere. Default: same origin. */
  readonly VITE_CODI_API_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
