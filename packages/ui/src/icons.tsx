/** Small line icons for `spec.portal.links[].icon` (simplified glyphs, not brand assets). */
import type { ReactNode } from "react";

const paths: Record<string, ReactNode> = {
  link: (
    <>
      <path d="M10 14a4.5 4.5 0 0 0 6.4 0l3-3a4.5 4.5 0 0 0-6.4-6.4l-1 1" />
      <path d="M14 10a4.5 4.5 0 0 0-6.4 0l-3 3a4.5 4.5 0 0 0 6.4 6.4l1-1" />
    </>
  ),
  website: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.5 2.6 3.8 5.6 3.8 9s-1.3 6.4-3.8 9c-2.5-2.6-3.8-5.6-3.8-9S9.5 5.6 12 3Z" />
    </>
  ),
  discord: (
    <>
      <path d="M7.5 7.5c3-1.3 6-1.3 9 0M7 16.5c3.3 1.4 6.7 1.4 10 0" />
      <path d="M8.5 5.5 7 4.5C5.3 5 4 5.8 3 7c-1 3.5-1.2 7 0 10.5 1.2.9 2.6 1.6 4 2l1.3-2M15.5 5.5l1.5-1c1.7.5 3 1.3 4 2.5 1 3.5 1.2 7 0 10.5-1.2.9-2.6 1.6-4 2l-1.3-2" />
      <circle cx="9.3" cy="12.2" r="1.2" />
      <circle cx="14.7" cy="12.2" r="1.2" />
    </>
  ),
  youtube: (
    <>
      <rect x="2.5" y="5.5" width="19" height="13" rx="4" />
      <path d="m10.3 9.3 4.6 2.7-4.6 2.7Z" />
    </>
  ),
  twitch: (
    <>
      <path d="M4.5 3.5h16v10.5l-4 4h-4l-3 3v-3h-5Z" />
      <path d="M11 8v4M15.5 8v4" />
    </>
  ),
  x: <path d="M4 4l16 16M20 4 4 20" />,
  bluesky: (
    <path d="M12 11c-1.6-3.2-5.4-7-7.6-6.6C2.7 4.7 3 8 3.6 10c.8 2.4 3.4 3 5.6 2.6-3.6.8-4 3.2-1.8 5 3 2.4 4.2-1.2 4.6-3 .4 1.8 1.6 5.4 4.6 3 2.2-1.8 1.8-4.2-1.8-5 2.2.4 4.8-.2 5.6-2.6.6-2 .9-5.3-.8-5.6C17.4 4 13.6 7.8 12 11Z" />
  ),
  mastodon: (
    <>
      <path d="M20.5 9.5c0-4.4-2.9-5.7-2.9-5.7C16.2 3.1 13.7 3 12 3s-4.2.1-5.6.8c0 0-2.9 1.3-2.9 5.7 0 5.2-.3 11.4 9.7 10 0 0-1.9-.3-2.7-1.5" />
      <path d="M8 14.5V9.3a2 2 0 0 1 4 0v3M12 12.3V9.3a2 2 0 0 1 4 0v5.2" />
    </>
  ),
  instagram: (
    <>
      <rect x="3.5" y="3.5" width="17" height="17" rx="5" />
      <circle cx="12" cy="12" r="3.8" />
      <circle cx="17.2" cy="6.8" r=".6" />
    </>
  ),
  tiktok: <path d="M13.5 3v11.5a3.5 3.5 0 1 1-3.5-3.5M13.5 3c.4 2.8 2.2 4.6 5 5" />,
  github: (
    <path d="M9 19c-4.3 1.4-4.3-2.5-6-3m12 5v-3.5c0-1 .1-1.4-.5-2 2.8-.3 5.5-1.4 5.5-6a4.6 4.6 0 0 0-1.3-3.2 4.2 4.2 0 0 0-.1-3.2s-1.1-.3-3.5 1.3a12.3 12.3 0 0 0-6.2 0C6.5 2.8 5.4 3.1 5.4 3.1a4.2 4.2 0 0 0-.1 3.2A4.6 4.6 0 0 0 4 9.5c0 4.6 2.7 5.7 5.5 6-.6.6-.6 1.2-.5 2V21" />
  ),
  reddit: (
    <>
      <ellipse cx="12" cy="14.5" rx="8" ry="5.5" />
      <path d="M12 9l1.3-5 4 1M9.5 16.5c1.5 1 3.5 1 5 0" />
      <circle cx="18.5" cy="5.5" r="1.3" />
      <circle cx="9" cy="13.5" r=".6" />
      <circle cx="15" cy="13.5" r=".6" />
    </>
  ),
  steam: (
    <>
      <circle cx="12" cy="12" r="9" />
      <circle cx="15.5" cy="9.5" r="2.5" />
      <circle cx="9" cy="15.5" r="1.8" />
      <path d="m3.5 13.5 4 1.7M10.6 14.6l3-3" />
    </>
  ),
  rules: (
    <>
      <path d="M6 3.5h9l3 3v14H6Z" />
      <path d="M9 10h6M9 13.5h6M9 17h3.5" />
    </>
  ),
  calendar: (
    <>
      <rect x="3.5" y="5" width="17" height="15.5" rx="2.5" />
      <path d="M3.5 10h17M8 3v4M16 3v4" />
    </>
  ),
  shop: (
    <>
      <path d="M4 8h16l-1.3 11.5a1.5 1.5 0 0 1-1.5 1.5H6.8a1.5 1.5 0 0 1-1.5-1.5Z" />
      <path d="M8.5 10V7a3.5 3.5 0 0 1 7 0v3" />
    </>
  ),
};

export interface LinkIconProps {
  /** Icon name (`spec.portal.links[].icon`); unknown names fall back to "link". */
  name: string;
  size?: number;
  className?: string;
}

export function LinkIcon({ name, size = 20, className }: LinkIconProps) {
  return (
    <svg
      className={["codi-link-icon", className].filter(Boolean).join(" ")}
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      data-icon={name in paths ? name : "link"}
    >
      {paths[name] ?? paths["link"]}
    </svg>
  );
}

/** Icon names known by `LinkIcon`. */
export const LINK_ICONS = Object.keys(paths);
