/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Origin of the CODI runtime when the portal is hosted elsewhere (CORS). Default: same origin. */
  readonly VITE_CODI_API_URL?: string;
  /** Server key to show when the URL has no ?server= (default: the first portal server). */
  readonly VITE_CODI_SERVER?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
