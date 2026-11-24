/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Origin of the Cicord runtime when the admin is served elsewhere. Default: same origin. */
  readonly VITE_CICORD_API_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
