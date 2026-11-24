/**
 * A small, safe Markdown subset for texts written in the server YAML
 * (`spec.portal.description`): paragraphs, `#`–`###` headings, `-`/`*`/`1.` lists, `>` quotes,
 * `**bold**`, `*italic*` / `_italic_`, `` `code` `` and `[links](https://…)`.
 *
 * It builds React elements: raw HTML is shown as text, and links are kept only for http(s)
 * URLs (opened in a new tab, `rel="noopener noreferrer"`), so YAML authors cannot inject
 * script into the portal.
 */
import type { ReactNode } from "react";

export type MarkdownBlock =
  | { type: "heading"; level: 1 | 2 | 3; text: string }
  | { type: "paragraph"; text: string }
  | { type: "quote"; text: string }
  | { type: "list"; ordered: boolean; items: string[] };

/** Splits Markdown source into blocks. */
export function parseMarkdown(source: string): MarkdownBlock[] {
  const blocks: MarkdownBlock[] = [];
  let para: string[] = [];
  let quote: string[] = [];
  let list: { ordered: boolean; items: string[] } | null = null;
  const flush = () => {
    if (para.length) blocks.push({ type: "paragraph", text: para.join(" ") });
    if (quote.length) blocks.push({ type: "quote", text: quote.join(" ") });
    if (list) blocks.push({ type: "list", ...list });
    para = [];
    quote = [];
    list = null;
  };
  for (const raw of source.replace(/\r\n?/g, "\n").split("\n")) {
    const line = raw.trim();
    if (line === "") {
      flush();
      continue;
    }
    const heading = /^(#{1,3})\s+(.+)$/.exec(line);
    if (heading) {
      flush();
      blocks.push({
        type: "heading",
        level: heading[1]!.length as 1 | 2 | 3,
        text: heading[2]!.replace(/\s+#+$/, ""),
      });
      continue;
    }
    const bullet = /^[-*+]\s+(.*)$/.exec(line);
    const numbered = /^\d{1,3}[.)]\s+(.*)$/.exec(line);
    if (bullet || numbered) {
      const ordered = !bullet;
      if (!list || list.ordered !== ordered) {
        flush();
        list = { ordered, items: [] };
      }
      list.items.push((bullet ?? numbered)![1]!);
      continue;
    }
    if (line.startsWith(">")) {
      if (!quote.length) flush();
      quote.push(line.replace(/^>\s?/, ""));
      continue;
    }
    if (list) {
      // A continuation line of the last item.
      const items = list.items;
      items[items.length - 1] += ` ${line}`;
      continue;
    }
    if (quote.length) flush();
    para.push(line);
  }
  flush();
  return blocks;
}

const SAFE_URL = /^https?:\/\//i;

/** Inline formatting → React nodes. */
export function renderInline(text: string, keyPrefix = "i"): ReactNode[] {
  const out: ReactNode[] = [];
  // Order matters: code first (no formatting inside), then links, bold, italic.
  // Every run is bounded ({1,MAX}): an unbounded [^*]* scanned to the end of the text from
  // each "*" without a closing one, quadratic on hostile input.
  const re =
    /(`([^`]{1,1000})`)|(\[([^\]]{1,1000})\]\(([^)\s]{1,2000})\))|(\*\*([^*]{1,1000})\*\*)|(__([^_]{1,1000})__)|(\*([^*\s][^*]{0,999})\*)|(\b_([^_\s][^_]{0,999})_\b)/g;
  let last = 0;
  let n = 0;
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const key = `${keyPrefix}-${n++}`;
    if (m[1]) out.push(<code key={key}>{m[2]}</code>);
    else if (m[3]) {
      const href = m[5]!;
      out.push(
        SAFE_URL.test(href) ? (
          <a key={key} href={href} target="_blank" rel="noopener noreferrer">
            {renderInline(m[4]!, key)}
          </a>
        ) : (
          m[4]!
        ),
      );
    } else if (m[6] || m[8])
      out.push(<strong key={key}>{renderInline((m[7] ?? m[9])!, key)}</strong>);
    else out.push(<em key={key}>{renderInline((m[11] ?? m[13])!, key)}</em>);
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

export interface MarkdownProps {
  source: string;
  /** Heading level of `#` (default 2: the page title is the h1). */
  baseLevel?: 1 | 2 | 3 | 4;
  className?: string;
}

export function Markdown({ source, baseLevel = 2, className }: MarkdownProps) {
  const blocks = parseMarkdown(source);
  if (!blocks.length) return null;
  return (
    <div className={["cicord-markdown", className].filter(Boolean).join(" ")}>
      {blocks.map((b, i) => {
        const key = `b${i}`;
        switch (b.type) {
          case "heading": {
            const Tag = `h${Math.min(6, baseLevel + b.level - 1)}` as "h2";
            return <Tag key={key}>{renderInline(b.text, key)}</Tag>;
          }
          case "quote":
            return (
              <blockquote key={key}>
                <p>{renderInline(b.text, key)}</p>
              </blockquote>
            );
          case "list": {
            const Tag = b.ordered ? "ol" : "ul";
            return (
              <Tag key={key}>
                {b.items.map((item, j) => (
                  <li key={j}>{renderInline(item, `${key}-${j}`)}</li>
                ))}
              </Tag>
            );
          }
          default:
            return <p key={key}>{renderInline(b.text, key)}</p>;
        }
      })}
    </div>
  );
}
