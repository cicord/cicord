/**
 * Tiny, safe Markdown renderer (Discord flavour) producing React nodes — never HTML strings.
 * Supports paragraphs, line breaks, **bold**, *italic* / _italic_, __underline__, ~~strike~~,
 * `code`, ```code blocks```, [links](https://…), bare URLs, > quotes, - lists, # headings,
 * and Discord mentions (<@id>, <@&id>, <#id>) through `resolveMention`.
 */
import { Fragment, type ReactNode } from "react";

export interface MarkdownOptions {
  resolveMention?: (kind: "user" | "role" | "channel", id: string) => string | undefined;
  /** Called for links; default opens in a new tab. */
  onLink?: (url: string) => void;
  /** Highlight `{placeholder}` tokens (message templates). */
  placeholders?: boolean;
}

const INLINE =
  /(```[\s\S]+?```|`[^`\n]+`|\*\*[^*\n]+\*\*|__[^_\n]+__|~~[^~\n]+~~|\*[^*\n]+\*|_[^_\n]+_|\[[^\]\n]+\]\((?:https?:\/\/|\/)[^)\s]+\)|<@!?[\w-]+>|<@&[\w-]+>|<#[\w-]+>|https?:\/\/[^\s<>()]+|\{[a-zA-Z][\w.]*\})/g;

export function isSafeUrl(url: string): boolean {
  return /^(https?:\/\/|\/)/i.test(url);
}

function inline(text: string, opts: MarkdownOptions, key: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let i = 0;
  for (const match of text.matchAll(INLINE)) {
    const token = match[0];
    const start = match.index;
    if (start > last) out.push(text.slice(last, start));
    last = start + token.length;
    const k = `${key}-${i++}`;
    if (token.startsWith("```")) out.push(<code key={k}>{token.slice(3, -3)}</code>);
    else if (token.startsWith("`")) out.push(<code key={k}>{token.slice(1, -1)}</code>);
    else if (token.startsWith("**"))
      out.push(<strong key={k}>{inline(token.slice(2, -2), opts, k)}</strong>);
    else if (token.startsWith("__")) out.push(<u key={k}>{inline(token.slice(2, -2), opts, k)}</u>);
    else if (token.startsWith("~~")) out.push(<s key={k}>{inline(token.slice(2, -2), opts, k)}</s>);
    else if (token.startsWith("*") || token.startsWith("_"))
      out.push(<em key={k}>{inline(token.slice(1, -1), opts, k)}</em>);
    else if (token.startsWith("[")) {
      const m = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(token);
      out.push(m ? link(m[2]!, m[1]!, opts, k) : token);
    } else if (token.startsWith("<@&")) {
      const id = token.slice(3, -1);
      out.push(mention("role", id, opts, k));
    } else if (token.startsWith("<@")) {
      const id = token.replace(/^<@!?/, "").slice(0, -1);
      out.push(mention("user", id, opts, k));
    } else if (token.startsWith("<#")) out.push(mention("channel", token.slice(2, -1), opts, k));
    else if (token.startsWith("{")) {
      out.push(
        opts.placeholders ? (
          <span key={k} className="md-placeholder">
            {token}
          </span>
        ) : (
          token
        ),
      );
    } else out.push(link(token, token, opts, k));
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

function link(url: string, label: string, opts: MarkdownOptions, key: string): ReactNode {
  if (!isSafeUrl(url)) return label;
  return (
    <a
      key={key}
      href={url}
      target="_blank"
      rel="noreferrer noopener"
      onClick={
        opts.onLink
          ? (e) => {
              e.preventDefault();
              opts.onLink!(url);
            }
          : undefined
      }
    >
      {label}
    </a>
  );
}

function mention(
  kind: "user" | "role" | "channel",
  id: string,
  opts: MarkdownOptions,
  key: string,
): ReactNode {
  const name = opts.resolveMention?.(kind, id);
  const prefix = kind === "channel" ? "#" : "@";
  return (
    <span key={key} className="md-mention" data-kind={kind}>
      {prefix}
      {name ?? (kind === "channel" ? "unknown-channel" : id)}
    </span>
  );
}

function lines(text: string, opts: MarkdownOptions, key: string): ReactNode[] {
  return text.split("\n").flatMap((line, i, all) => {
    const nodes = inline(line, opts, `${key}-${i}`);
    return i < all.length - 1 ? [...nodes, <br key={`${key}-br-${i}`} />] : nodes;
  });
}

export function Markdown({ source, ...opts }: { source: string } & MarkdownOptions) {
  const blocks = source.replace(/\r\n/g, "\n").split(/\n{2,}/);
  return (
    <div className="md">
      {blocks.map((block, i) => {
        const key = `b${i}`;
        if (block.startsWith("```") && block.endsWith("```"))
          return (
            <pre key={key}>
              <code>{block.replace(/^```\w*\n?/, "").replace(/```$/, "")}</code>
            </pre>
          );
        const heading = /^(#{1,3})\s+(.*)$/.exec(block);
        if (heading && !block.includes("\n")) {
          const level = heading[1]!.length;
          const content = inline(heading[2]!, opts, key);
          return level === 1 ? (
            <h3 key={key}>{content}</h3>
          ) : level === 2 ? (
            <h4 key={key}>{content}</h4>
          ) : (
            <h5 key={key}>{content}</h5>
          );
        }
        if (block.split("\n").every((l) => /^>\s?/.test(l)))
          return (
            <blockquote key={key}>{lines(block.replace(/^>\s?/gm, ""), opts, key)}</blockquote>
          );
        if (block.split("\n").every((l) => /^[-*]\s+/.test(l)))
          return (
            <ul key={key}>
              {block.split("\n").map((l, j) => (
                <li key={j}>{inline(l.replace(/^[-*]\s+/, ""), opts, `${key}-${j}`)}</li>
              ))}
            </ul>
          );
        return (
          <Fragment key={key}>
            <p>{lines(block, opts, key)}</p>
          </Fragment>
        );
      })}
    </div>
  );
}
