/** Discord-like rendering of simulator messages: content, embeds, buttons and selects. */
import type {
  Message,
  MessageComponent,
  MessageButton,
  MessageEntitySelect,
  MessageEmbed,
  MessageSelect,
  User,
} from "@cicord/client";
import { useState } from "react";
import { Markdown, isSafeUrl } from "../schema-form/index.ts";
import { IconExternal, IconEye } from "./kit.tsx";
import { Avatar, Spinner } from "./kit.tsx";
import { i18n } from "../i18n/index.ts";

export interface MentionResolver {
  user: (id: string) => string | undefined;
  role: (id: string) => string | undefined;
  channel: (id: string) => string | undefined;
}

export interface MessageListProps {
  messages: Message[];
  resolver: MentionResolver;
  /** Viewer of ephemeral messages (they are shown as "only visible to …"). */
  viewer?: User | undefined;
  onComponent?: (message: Message, customId: string, values: string[]) => Promise<void>;
  /** Message ids with an interaction in flight. */
  busy?: string | null;
  botIds?: ReadonlySet<string>;
}

const timeFmt = () =>
  new Intl.DateTimeFormat(i18n.language, { hour: "2-digit", minute: "2-digit" });

function when(iso: string): string {
  const dayFmt = new Intl.DateTimeFormat(i18n.language, { month: "short", day: "numeric" });
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const today = new Date();
  return d.toDateString() === today.toDateString()
    ? i18n.t("sim.time.today", { time: timeFmt().format(d) })
    : i18n.t("sim.time.at", { day: dayFmt.format(d), time: timeFmt().format(d) });
}

/** Consecutive messages of the same author within 5 minutes are grouped. */
function grouped(prev: Message | undefined, m: Message): boolean {
  if (!prev || prev.author.id !== m.author.id || prev.ephemeral !== m.ephemeral) return false;
  return Date.parse(m.createdAt) - Date.parse(prev.createdAt) < 5 * 60000;
}

export function MessageList(props: MessageListProps) {
  return (
    <ol className="msg-list">
      {props.messages.map((m, i) => (
        <MessageItem
          key={m.id}
          message={m}
          compact={grouped(props.messages[i - 1], m)}
          {...props}
        />
      ))}
    </ol>
  );
}

export function MessageItem({
  message,
  compact,
  resolver,
  viewer,
  onComponent,
  busy,
  botIds,
}: Omit<MessageListProps, "messages"> & { message: Message; compact?: boolean }) {
  const m = message;
  const isBot = (m.author as User & { bot?: boolean }).bot || botIds?.has(m.author.id);
  const name = m.author.displayName || m.author.username;
  const edited = m.editedAt;
  return (
    <li
      className="msg"
      data-compact={compact || undefined}
      data-ephemeral={m.ephemeral || undefined}
    >
      {compact ? (
        <span className="msg-gutter-time" aria-hidden="true">
          {timeFmt().format(new Date(m.createdAt))}
        </span>
      ) : (
        <Avatar name={name} url={m.author.avatarUrl} size={36} bot={isBot} />
      )}
      <div className="msg-main">
        {compact ? null : (
          <div className="msg-head">
            <span className="msg-author">{name}</span>
            {isBot ? <span className="msg-bot-tag">{i18n.t("sim.bot")}</span> : null}
            <time className="msg-time" dateTime={m.createdAt}>
              {when(m.createdAt)}
            </time>
          </div>
        )}
        {m.content ? (
          <div className="msg-content">
            <Markdown source={m.content} resolveMention={(kind, id) => resolver[kind](id)} />
            {edited ? <span className="msg-edited">{i18n.t("sim.edited")}</span> : null}
          </div>
        ) : null}
        {(m.embeds ?? []).map((e, i) => (
          <Embed key={i} embed={e} resolver={resolver} />
        ))}
        {(m.components ?? []).length ? (
          <div className="msg-components">
            {m.components.map((node, index) => (
              <ComponentView
                key={index}
                node={node}
                resolver={resolver}
                disabled={!onComponent || busy === m.id}
                onPick={(id, values) => onComponent?.(m, id, values)}
              />
            ))}
          </div>
        ) : null}
        {m.ephemeral ? (
          <p className="msg-ephemeral">
            <IconEye size={12} /> Only{" "}
            {viewer ? viewer.displayName || viewer.username : "the invoking user"} can see this
          </p>
        ) : null}
      </div>
    </li>
  );
}

function ComponentView({
  node,
  resolver,
  disabled,
  onPick,
}: {
  node: MessageComponent;
  resolver: MentionResolver;
  disabled: boolean;
  onPick: (id: string, values: string[]) => Promise<void> | undefined;
}) {
  const children = (nodes?: MessageComponent[]) =>
    nodes?.map((n, index) => (
      <ComponentView key={index} node={n} resolver={resolver} disabled={disabled} onPick={onPick} />
    ));
  switch (node.type) {
    case "button": {
      const customId = node.customId;
      return (
        <ComponentButton
          button={node}
          busy={disabled}
          onClick={
            customId
              ? async () => {
                  await onPick(customId, []);
                }
              : undefined
          }
        />
      );
    }
    case "select":
      return (
        <SelectMenu select={node} disabled={disabled} onPick={(v) => onPick(node.customId, v)} />
      );
    case "userSelect":
    case "roleSelect":
    case "channelSelect":
    case "mentionableSelect":
      return (
        <EntityInput select={node} disabled={disabled} onPick={(v) => onPick(node.customId, v)} />
      );
    case "text":
      return <Markdown source={node.content ?? ""} resolveMention={(k, id) => resolver[k](id)} />;
    case "separator":
      return node.divider === false ? <br /> : <hr />;
    case "thumbnail":
      return node.media && isSafeUrl(node.media.url) ? (
        <img className="embed-thumb" src={node.media.url} alt={node.description ?? ""} />
      ) : null;
    case "mediaGallery":
      return (
        <div>
          {node.items?.map((item, index) =>
            isSafeUrl(item.media.url) ? (
              <img
                key={index}
                className="embed-image"
                src={item.media.url}
                alt={item.description ?? ""}
              />
            ) : null,
          )}
        </div>
      );
    case "file":
      return <span>{node.file?.url}</span>;
    case "row":
      return <div className="msg-row">{children(node.components)}</div>;
    case "section":
    case "container":
      return (
        <div className="msg-embed">
          {children(node.components)}
          {node.accessory ? children([node.accessory]) : null}
        </div>
      );
  }
  return null;
}

function EntityInput({
  select,
  disabled,
  onPick,
}: {
  select: MessageEntitySelect;
  disabled: boolean;
  onPick: (v: string[]) => unknown;
}) {
  const [raw, setRaw] = useState(() => select.default_values?.map((v) => v.id).join(", ") ?? "");
  const ids = raw.split(/[ ,]+/).filter(Boolean);
  return (
    <form
      className="msg-row"
      onSubmit={(e) => {
        e.preventDefault();
        onPick(ids);
      }}
    >
      <input
        className="input"
        placeholder={select.placeholder ?? "Discord IDs"}
        aria-label={select.placeholder ?? select.type}
        value={raw}
        onChange={(e) => setRaw(e.target.value)}
        disabled={disabled}
      />
      <button
        className="btn"
        disabled={
          disabled || ids.length < (select.minValues ?? 1) || ids.length > (select.maxValues ?? 1)
        }
      >
        {i18n.t("sim.submit")}
      </button>
    </form>
  );
}

function Embed({ embed, resolver }: { embed: MessageEmbed; resolver: MentionResolver }) {
  const e = embed;
  const image = e.imageUrl;
  const thumb = e.thumbnailUrl;
  return (
    <div className="embed" style={{ borderLeftColor: e.color || undefined }}>
      <div className="embed-body">
        {e.author?.name ? <div className="embed-author">{e.author.name}</div> : null}
        {e.title ? (
          <div className="embed-title">
            {e.url && isSafeUrl(e.url) ? (
              <a href={e.url} target="_blank" rel="noreferrer">
                {e.title}
              </a>
            ) : (
              e.title
            )}
          </div>
        ) : null}
        {e.description ? (
          <div className="embed-desc">
            <Markdown source={e.description} resolveMention={(k, id) => resolver[k](id)} />
          </div>
        ) : null}
        {e.fields?.length ? (
          <div className="embed-fields">
            {e.fields.map((f, i) => (
              <div key={i} className="embed-field" data-inline={f.inline || undefined}>
                <div className="embed-field-name">{f.name}</div>
                <div className="embed-field-value">
                  <Markdown source={f.value} resolveMention={(k, id) => resolver[k](id)} />
                </div>
              </div>
            ))}
          </div>
        ) : null}
        {image && isSafeUrl(image) ? <img className="embed-image" src={image} alt="" /> : null}
        {e.footer?.text ? <div className="embed-footer">{e.footer.text}</div> : null}
      </div>
      {thumb && isSafeUrl(thumb) ? <img className="embed-thumb" src={thumb} alt="" /> : null}
    </div>
  );
}

function ComponentButton({
  button,
  busy,
  onClick,
}: {
  button: MessageButton;
  busy: boolean;
  onClick: (() => Promise<void>) | undefined;
}) {
  const b = button as Omit<MessageButton, "type"> & { type: string };
  const link = b.style === "link" || b.type === "link" || (!!b.url && !b.customId);
  const style = link ? "link" : b.style || "secondary";
  const content = (
    <>
      {b.emoji ? <span className="btn-emoji">{b.emoji}</span> : null}
      {b.label ? <span>{b.label}</span> : null}
      {link ? <IconExternal size={12} /> : null}
    </>
  );
  if (link) {
    if (!b.url || !isSafeUrl(b.url))
      return (
        <span className="dbtn dbtn-link" aria-disabled="true">
          {content}
        </span>
      );
    return (
      <a
        className="dbtn dbtn-link"
        href={b.url}
        target="_blank"
        rel="noreferrer"
        title={b.url}
        aria-disabled={b.disabled || undefined}
      >
        {content}
      </a>
    );
  }
  return (
    <button
      type="button"
      className={`dbtn dbtn-${style}`}
      disabled={b.disabled || busy || !onClick}
      onClick={() => void onClick?.()}
      title={b.customId ? `customId: ${b.customId}` : undefined}
    >
      {busy ? <Spinner size={12} /> : null}
      {content}
    </button>
  );
}

function SelectMenu({
  select,
  disabled,
  onPick,
}: {
  select: MessageSelect;
  disabled: boolean;
  onPick: (values: string[]) => void;
}) {
  const multi = (select.maxValues ?? 1) > 1;
  const [picked, setPicked] = useState<string[]>(
    select.options.filter((o) => o.default).map((o) => o.value),
  );
  if (!multi)
    return (
      <select
        className="dselect"
        disabled={disabled}
        value=""
        aria-label={select.placeholder || i18n.t("sim.select.option")}
        onChange={(e) => e.target.value && onPick([e.target.value])}
      >
        <option value="">{select.placeholder || i18n.t("sim.select.make")}</option>
        {select.options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.emoji ? `${o.emoji} ` : ""}
            {o.label}
            {o.description ? ` — ${o.description}` : ""}
          </option>
        ))}
      </select>
    );
  return (
    <fieldset className="dselect-multi" disabled={disabled}>
      <legend>{select.placeholder || i18n.t("sim.select.pick")}</legend>
      {select.options.map((o) => (
        <label key={o.value}>
          <input
            type="checkbox"
            checked={picked.includes(o.value)}
            onChange={(e) =>
              setPicked((p) =>
                e.target.checked ? [...p, o.value] : p.filter((v) => v !== o.value),
              )
            }
          />
          {o.emoji ? `${o.emoji} ` : ""}
          {o.label}
        </label>
      ))}
      <button
        type="button"
        className="dbtn dbtn-primary"
        disabled={
          picked.length < (select.minValues ?? 1) || picked.length > (select.maxValues ?? 25)
        }
        onClick={() => onPick(picked)}
      >
        {i18n.t("sim.submit")}
      </button>
    </fieldset>
  );
}
