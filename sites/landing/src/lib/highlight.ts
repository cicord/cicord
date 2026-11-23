// A deliberately small highlighter for the handful of YAML and TypeScript snippets on the
// landing page (no dependency, no client JS). It only colors keys, strings, comments and
// keywords; the snippets are written so that this is enough.

function escape(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

const span = (cls: string, s: string) => `<span class="${cls}">${s}</span>`;

/** Highlights one line of YAML. */
export function yamlLine(line: string): string {
  const raw = escape(line);
  // Split off a trailing comment ("#" at the start or after whitespace, outside quotes).
  let code = raw;
  let comment = "";
  const m = /(^|\s)(#.*)$/.exec(raw);
  if (m && !/"[^"]*$/.test(raw.slice(0, m.index + m[1].length))) {
    code = raw.slice(0, m.index + m[1].length);
    comment = span("t-com", m[2]);
  }
  code = code.replace(
    /^(\s*(?:- )?)([\w@.-]+|"[^"]*")(:)(?=\s|$)/,
    (_, lead: string, key: string, colon: string) => lead + span("t-key", key) + colon,
  );
  code = code.replace(/("(?:[^"\\]|\\.)*")(?=\s*(?:[,\]}]|$))/g, (s) => span("t-str", s));
  return code + comment;
}

const TS_TOKENS =
  /(\/\/.*$)|("(?:[^"\\]|\\.)*"|`(?:[^`\\]|\\.)*`)|\b(import|from|export|default|async|await|const|if|return)\b/gm;

/** Highlights TypeScript source. */
export function ts(source: string): string {
  return escape(source).replace(TS_TOKENS, (all, com?: string, str?: string, kw?: string) => {
    if (com) return span("t-com", com);
    if (str) return span("t-str", str);
    if (kw) return span("t-kw", kw);
    return all;
  });
}

/** Highlights a YAML document. */
export function yaml(source: string): string {
  return source.split("\n").map(yamlLine).join("\n");
}
