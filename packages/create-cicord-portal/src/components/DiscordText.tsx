/**
 * Discord message content as React elements: user, role and channel mentions become chips
 * (named when the page knows the name, otherwise a neutral "@user" / "@role" / "#channel"
 * that never shows the raw ID), custom emojis show their `:name:`, command mentions their
 * `/name`. Minimal formatting: `**bold**`, `__underline__`, `*italic*` / `_italic_`,
 * `` `code` ``, ```` ```code blocks``` ```` and line breaks.
 *
 * Everything is built as React elements (no HTML string is ever injected), so message
 * content cannot add markup or script to the page.
 */
import type { ReactNode } from "react";

export interface MentionNames {
  /** User ID → display name. */
  users?: ReadonlyMap<string, string>;
  /** Role ID → role name. */
  roles?: ReadonlyMap<string, string>;
  /** Channel ID → channel name. */
  channels?: ReadonlyMap<string, string>;
}

/** Shown for a mention whose name is unknown (localized: "user", "role", "channel"). */
export interface MentionFallbacks {
  user: string;
  role: string;
  channel: string;
}

// Every run is bounded ({1,MAX}) so a "*" or "`" without its closing pair cannot make the
// scan quadratic on hostile input.
const INLINE = new RegExp(
  [
    /`([^`\n]{1,2000})`/.source, // 1 code
    /<(@!?|@&|#)(\d{5,25})>/.source, // 2 kind, 3 id
    /<a?:(\w{1,32}):\d{5,25}>/.source, // 4 custom emoji
    /<\/([\w-]{1,32}(?: [\w-]{1,32}){0,2}):\d{5,25}>/.source, // 5 command
    /\*\*([^*]{1,2000})\*\*/.source, // 6 bold
    /__([^_]{1,2000})__/.source, // 7 underline
    /\*([^*\s][^*]{0,1999})\*/.source, // 8 italic
    /\b_([^_\s][^_]{0,1999})_\b/.source, // 9 italic
  ].join("|"),
  "g",
);

function inline(
  text: string,
  names: MentionNames,
  fallback: MentionFallbacks,
  key: string,
): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let n = 0;
  for (const m of text.matchAll(INLINE)) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const k = `${key}-${n++}`;
    if (m[1] !== undefined) out.push(<code key={k}>{m[1]}</code>);
    else if (m[3] !== undefined) {
      const id = m[3];
      const label =
        m[2] === "#"
          ? "#" + (names.channels?.get(id) ?? fallback.channel)
          : "@" +
            ((m[2] === "@&" ? names.roles?.get(id) : names.users?.get(id)) ??
              (m[2] === "@&" ? fallback.role : fallback.user));
      out.push(
        <span key={k} className="mention">
          {label}
        </span>,
      );
    } else if (m[4] !== undefined) out.push(`:${m[4]}:`);
    else if (m[5] !== undefined)
      out.push(
        <span key={k} className="mention">
          /{m[5]}
        </span>,
      );
    else if (m[6] !== undefined)
      out.push(<strong key={k}>{inline(m[6], names, fallback, k)}</strong>);
    else if (m[7] !== undefined) out.push(<u key={k}>{inline(m[7], names, fallback, k)}</u>);
    else out.push(<em key={k}>{inline((m[8] ?? m[9])!, names, fallback, k)}</em>);
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/** Renders Discord message content (see the module comment). */
function renderDiscord(
  content: string,
  names: MentionNames,
  fallback: MentionFallbacks,
): ReactNode[] {
  const out: ReactNode[] = [];
  // Code blocks first: nothing inside them is formatted.
  const parts = content.replace(/\r\n?/g, "\n").split(/```(?:[\w+-]{0,20}\n)?([\s\S]{0,8000}?)```/);
  parts.forEach((part, i) => {
    if (i % 2 === 1) {
      out.push(
        <code key={`b${i}`} className="code-block">
          {part.replace(/\n$/, "")}
        </code>,
      );
      return;
    }
    part.split("\n").forEach((line, j) => {
      if (j > 0) out.push(<br key={`b${i}-${j}`} />);
      out.push(...inline(line, names, fallback, `b${i}-${j}`));
    });
  });
  return out;
}

export function DiscordText({
  content,
  names,
  fallback,
}: {
  content: string;
  names: MentionNames;
  fallback: MentionFallbacks;
}) {
  return <>{renderDiscord(content, names, fallback)}</>;
}
