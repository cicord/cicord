/** Inline 16px stroke icons (no icon font, no dependency). */
import type { SVGProps } from "react";

type IconProps = SVGProps<SVGSVGElement> & { size?: number };

function make(paths: string, opts: { fill?: boolean } = {}) {
  return function Icon({ size = 16, ...rest }: IconProps) {
    return (
      <svg
        viewBox="0 0 16 16"
        width={size}
        height={size}
        fill={opts.fill ? "currentColor" : "none"}
        stroke={opts.fill ? "none" : "currentColor"}
        strokeWidth="1.4"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        {...rest}
      >
        {paths.split("|").map((d) => (
          <path key={d} d={d} />
        ))}
      </svg>
    );
  };
}

export const IconCommit = make("M8 5.5a2.5 2.5 0 110 5 2.5 2.5 0 010-5z|M1.5 8h4|M10.5 8h4");
export const IconBranch = make(
  "M4.5 2.5v11|M4.5 10.5c0-3 7-2 7-5.5|M11.5 2.5a1.5 1.5 0 110 3 1.5 1.5 0 010-3z",
);
export const IconServer = make("M2.5 3h11v4h-11z|M2.5 9h11v4h-11z|M5 5h.01|M5 11h.01");
export const IconBot = make(
  "M3.5 6h9v6.5h-9z|M8 3.5V6|M8 2.5h.01|M6 9h.01|M10 9h.01|M1.5 8.5v2|M14.5 8.5v2",
);
export const IconPlug = make("M5.5 1.5v3|M10.5 1.5v3|M3.5 4.5h9v3a4.5 4.5 0 01-9 0z|M8 12v2.5");
export const IconPlay = make("M5 3l8 5-8 5z");
export const IconSync = make(
  "M13.5 6A5.5 5.5 0 003.3 4.5|M2.5 10a5.5 5.5 0 0010.2 1.5|M13.5 2v4h-4|M2.5 14v-4h4",
);
export const IconCheck = make("M3 8.5l3 3 7-7");
export const IconX = make("M4 4l8 8|M12 4l-8 8");
export const IconChevronDown = make("M4 6l4 4 4-4");
export const IconChevronRight = make("M6 4l4 4-4 4");
export const IconExternal = make("M9 2.5h4.5V7|M13.5 2.5L7.5 8.5|M11.5 9.5v4h-9v-9h4");
export const IconAlert = make("M8 2l6.5 11.5h-13z|M8 6.5v3|M8 11.5h.01");
export const IconInfo = make("M8 1.5a6.5 6.5 0 110 13 6.5 6.5 0 010-13z|M8 7v4|M8 4.8h.01");
export const IconSun = make(
  "M8 5a3 3 0 110 6 3 3 0 010-6z|M8 1v1.5|M8 13.5V15|M1 8h1.5|M13.5 8H15|M3 3l1 1|M12 12l1 1|M3 13l1-1|M12 4l1-1",
);
export const IconMoon = make("M13.5 9.5A5.5 5.5 0 016.5 2.5a5.5 5.5 0 107 7z");
export const IconLogout = make("M6 2.5H3v11h3|M10 5l3 3-3 3|M13 8H6");
export const IconUser = make("M8 2a3 3 0 110 6 3 3 0 010-6z|M2.5 14a5.5 5.5 0 0111 0");
export const IconUsers = make(
  "M6 2.5a2.5 2.5 0 110 5 2.5 2.5 0 010-5z|M1.5 13.5a4.5 4.5 0 019 0|M10.5 3a2.5 2.5 0 010 4.5|M12 9.5a4.5 4.5 0 012.5 4",
);
export const IconHash = make("M6 2l-1.5 12|M11.5 2L10 14|M2.5 5.5h11|M2 10.5h11");
export const IconVolume = make("M2.5 6h2.5L8.5 3v10L5 10H2.5z|M11 5.5a3.5 3.5 0 010 5");
export const IconFolder = make("M1.5 4h4.5l1.5 1.5h7v7h-13z");
export const IconMail = make("M1.5 3.5h13v9h-13z|M1.5 4l6.5 5 6.5-5");
export const IconSend = make("M14.5 1.5l-6 13-2-5.5-5-2z|M14.5 1.5L6.5 9");
export const IconSlash = make("M10.5 2.5l-5 11");
export const IconPlus = make("M8 3v10|M3 8h10");
export const IconLock = make("M3.5 7h9v6.5h-9z|M5.5 7V5a2.5 2.5 0 015 0v2");
export const IconShield = make("M8 1.5l5.5 2v4c0 3.5-2.5 6-5.5 7-3-1-5.5-3.5-5.5-7v-4z");
export const IconClock = make("M8 1.5a6.5 6.5 0 110 13 6.5 6.5 0 010-13z|M8 4.5V8l2.5 1.5");
export const IconEye = make(
  "M1 8s2.5-5 7-5 7 5 7 5-2.5 5-7 5-7-5-7-5z|M8 6a2 2 0 110 4 2 2 0 010-4z",
);
export const IconDiff = make("M4.5 2v6|M1.5 5h6|M8.5 11h6|M11.5 14V8");
export const IconList = make("M5.5 4h8|M5.5 8h8|M5.5 12h8|M2.5 4h.01|M2.5 8h.01|M2.5 12h.01");
export const IconLogin = make("M10 2.5h3v11h-3|M6 5l3 3-3 3|M9 8H2");
export const IconRefresh = make("M13.5 8a5.5 5.5 0 11-1.6-3.9|M13.5 2v4h-4");
export const IconLeave = make("M10 2.5H13v11h-3|M6 5L3 8l3 3|M3 8h7");
export const IconTerminal = make("M1.5 2.5h13v11h-13z|M4 6l2 2-2 2|M8 10.5h4");
export const IconDiscord = make(
  "M5.5 3.5c-1.5.3-2.6.8-3.5 1.5-1 2.3-1.4 4.6-1 7 1 .8 2.2 1.3 3.5 1.5l.8-1.3M10.5 3.5c1.5.3 2.6.8 3.5 1.5 1 2.3 1.4 4.6 1 7-1 .8-2.2 1.3-3.5 1.5l-.8-1.3|M4.5 11.5c2.3 1 4.7 1 7 0|M6 8.5h.01|M10 8.5h.01|M5.5 3.5l.5 1c1.3-.3 2.7-.3 4 0l.5-1",
);
