/**
 * The playground (spec/api.md §8): act as simulated Discord users, watch bots answer live.
 */
import type { Json, Message, SimCommand, SimServer, User } from "@codi/client";
import { useCodi } from "@codi/react";
import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { keys, useSimCommands, useSimDms, useSimMessages, useSimState, useStatus } from "../api.ts";
import { useAdminConfig } from "../context.tsx";
import { Picker, Swatch, type PickerOption } from "../schema-form/index.ts";
import type { AdminInteractionResult, AdminSimServer, SimModal } from "../types.ts";
import {
  IconHash,
  IconLeave,
  IconMail,
  IconPlus,
  IconSend,
  IconSlash,
  IconUser,
  IconVolume,
  IconX,
} from "../ui/icons.tsx";
import {
  Avatar,
  Badge,
  Button,
  EmptyState,
  ErrorState,
  errorMessage,
  Skeleton,
  Spinner,
  useToast,
} from "../ui/index.tsx";
import { CommandRunner } from "./CommandRunner.tsx";
import { useSimLive } from "./live.ts";
import { MessageItem, MessageList, type MentionResolver } from "./MessageView.tsx";

type View = { kind: "channel"; id: string } | { kind: "dm"; userId: string };

interface LastInteraction {
  label: string;
  result: AdminInteractionResult;
  /** Context to submit a modal. */
  ctx: { userId: string; serverId: string; channelId: string; bot: string };
  at: number;
}

const ACTOR_KEY = "codi-admin:sim-actor";
const QUICKSTART_KEY = "codi-admin:sim-quickstart";

function stored(key: string): string | null {
  try {
    return sessionStorage.getItem(key);
  } catch {
    return null;
  }
}
function store(key: string, value: string) {
  try {
    sessionStorage.setItem(key, value);
  } catch {
    // ignore
  }
}

const TEXTUAL = new Set(["text", "announcement", "forum", "media", "", undefined]);

export function SimulatorPage() {
  const status = useStatus();
  const enabled = status.data?.simulator ?? true;
  const live = useSimLive(enabled);
  const state = useSimState(enabled);

  if (status.data && !status.data.simulator)
    return (
      <div className="page">
        <EmptyState icon={<IconSlash />} title="The simulator is off">
          Start the runtime with <code>codi dev --sim</code> to play with your bots without Discord.
        </EmptyState>
      </div>
    );
  if (state.isLoading)
    return (
      <div className="page">
        <Skeleton lines={10} />
      </div>
    );
  if (!state.data)
    return (
      <div className="page">
        <ErrorState error={state.error} onRetry={() => void state.refetch()} />
      </div>
    );
  if (state.data.servers.length === 0)
    return (
      <div className="page">
        <EmptyState title="No simulated server">The simulated world has no server yet.</EmptyState>
      </div>
    );
  return (
    <Playground
      users={state.data.users}
      bots={state.data.bots}
      servers={state.data.servers as AdminSimServer[]}
      live={live}
    />
  );
}

function Playground({
  users,
  bots,
  servers,
  live,
}: {
  users: User[];
  bots: User[];
  servers: AdminSimServer[];
  live: string;
}) {
  const { client, cache } = useCodi();
  const toast = useToast();
  const status = useStatus();
  const config = useAdminConfig();
  const [serverId, setServerId] = useState(servers[0]!.id);
  const server = servers.find((s) => s.id === serverId) ?? servers[0]!;
  const botIds = useMemo(() => new Set(bots.map((b) => b.id)), [bots]);
  const textChannels = server.channels.filter((c) => c.type !== "category" && TEXTUAL.has(c.type));
  const firstText = textChannels.find((c) => /g[eé]n[eé]ral/i.test(c.name)) ?? textChannels[0];
  const [view, setView] = useState<View | null>(
    firstText ? { kind: "channel", id: firstText.id } : null,
  );
  const [actorId, setActorId] = useState<string>(
    () => stored(ACTOR_KEY) ?? server.ownerId ?? users[0]?.id ?? "",
  );
  const [last, setLast] = useState<LastInteraction | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [modal, setModal] = useState<LastInteraction | null>(null);
  const [mode, setMode] = useState<"message" | "command">("message");
  const [commandHint, setCommandHint] = useState<string | undefined>(undefined);
  const [showQuick, setShowQuick] = useState(() => stored(QUICKSTART_KEY) !== "hidden");

  const allUsers = useMemo(() => [...users, ...bots], [users, bots]);
  const actor = users.find((u) => u.id === actorId) ?? users.find((u) => u.id === server.ownerId);
  const isMember = (id: string) => server.members.some((m) => m.user.id === id);
  const actorIsMember = actor ? isMember(actor.id) : false;

  useEffect(() => {
    if (actor) store(ACTOR_KEY, actor.id);
  }, [actor]);

  // Keep the selected channel valid when switching servers.
  useEffect(() => {
    if (view?.kind === "channel" && !server.channels.some((c) => c.id === view.id))
      setView(firstText ? { kind: "channel", id: firstText.id } : null);
  }, [server, view, firstText]);

  const resolver: MentionResolver = useMemo(
    () => ({
      user: (id) => {
        const u = allUsers.find((x) => x.id === id);
        const m = server.members.find((x) => x.user.id === id);
        return m?.nick || u?.displayName || u?.username;
      },
      role: (id) => server.roles.find((r) => r.id === id)?.name,
      channel: (id) => server.channels.find((c) => c.id === id)?.name,
    }),
    [allUsers, server],
  );

  const botKeyOf = (userId: string): string =>
    status.data?.bots.find((b) => b.userId === userId)?.key ?? userId;

  const refresh = () => void cache.invalidate(keys.sim());

  const join = async (user: User) => {
    try {
      await client.sim.join(server.id, user.id);
      toast.push({
        tone: "ok",
        title: `${user.displayName || user.username} joined ${server.name}`,
      });
      refresh();
    } catch (e) {
      toast.push({ tone: "bad", title: "Could not join", text: errorMessage(e) });
    }
  };
  const leave = async (user: User) => {
    try {
      await client.sim.leave(server.id, user.id);
      toast.push({
        tone: "info",
        title: `${user.displayName || user.username} left ${server.name}`,
      });
      refresh();
    } catch (e) {
      toast.push({ tone: "bad", title: "Could not leave", text: errorMessage(e) });
    }
  };

  const showResult = (
    label: string,
    result: AdminInteractionResult,
    ctx: LastInteraction["ctx"],
  ) => {
    const entry = { label, result, ctx, at: Date.now() };
    setLast(entry);
    if (result.status === "modal" && result.modal) setModal(entry);
    refresh();
  };

  const onComponent = async (message: Message, customId: string, values: string[]) => {
    if (!actor) return;
    setBusy(message.id);
    try {
      const result = (await client.sim.component({
        userId: actor.id,
        channelId: message.channelId,
        messageId: message.id,
        customId,
        values,
      })) as AdminInteractionResult;
      const component = message.components
        .flatMap((row) => row.components ?? [])
        .find((c) => c.customId === customId);
      const name =
        component && component.type === "button" ? component.label || customId : customId;
      const label = values.length ? `Picked ${values.join(", ")}` : `Clicked “${name}”`;
      showResult(label, result, {
        userId: actor.id,
        serverId: server.id,
        channelId: message.channelId,
        bot: botKeyOf(message.author.id),
      });
    } catch (e) {
      toast.push({ tone: "bad", title: "Interaction failed", text: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  };

  const channelForCommand = view?.kind === "channel" ? view.id : (firstText?.id ?? "");

  const runCommand = async (command: SimCommand, options: { [key: string]: Json }) => {
    if (!actor) return;
    try {
      const result = (await client.sim.command({
        userId: actor.id,
        serverId: server.id,
        channelId: channelForCommand,
        bot: command.bot,
        name: command.name,
        options,
      })) as AdminInteractionResult;
      showResult(`/${command.name}`, result, {
        userId: actor.id,
        serverId: server.id,
        channelId: channelForCommand,
        bot: command.bot,
      });
    } catch (e) {
      toast.push({ tone: "bad", title: `/${command.name} failed`, text: errorMessage(e) });
    }
  };

  const submitModal = async (
    entry: LastInteraction,
    modalDef: SimModal,
    fields: Record<string, string>,
  ) => {
    try {
      const result = await client.request<AdminInteractionResult>(
        "POST",
        `${client.apiPath}/sim/modals`,
        {
          body: { ...entry.ctx, customId: modalDef.customId, fields },
        },
      );
      setModal(null);
      showResult(`Submitted “${modalDef.title}”`, result, entry.ctx);
    } catch (e) {
      toast.push({ tone: "bad", title: "Modal submit failed", text: errorMessage(e) });
    }
  };

  const nonMembers = users.filter((u) => !isMember(u.id));
  const welcomeUser = nonMembers[0];
  const dmTarget = [...server.members]
    .reverse()
    .find((m) => !botIds.has(m.user.id) && m.user.id !== server.ownerId)?.user;

  return (
    <div className="sim">
      <aside className="sim-nav" aria-label="Channels">
        <div className="sim-server">
          {servers.length > 1 ? (
            <select
              className="input"
              value={server.id}
              aria-label="Simulated server"
              onChange={(e) => setServerId(e.target.value)}
            >
              {servers.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          ) : (
            <div className="sim-server-name">{server.name}</div>
          )}
          <span
            className={`live live-${live}`}
            title={
              live === "live"
                ? "Live updates (SSE)"
                : live === "polling"
                  ? "Live updates unavailable: polling every 2.5 s"
                  : "Connecting…"
            }
          >
            <span className="live-dot" />
            {live === "live" ? "Live" : live === "polling" ? "Polling" : "…"}
          </span>
        </div>
        <ChannelNav server={server} view={view} onSelect={setView} />
        <div className="sim-nav-section">
          <h3 className="sim-nav-title">Direct messages</h3>
          <p className="muted small sim-nav-hint">What bots sent to each user</p>
          <ul className="sim-dm-list">
            {users.map((u) => (
              <DmEntry
                key={u.id}
                user={u}
                active={view?.kind === "dm" && view.userId === u.id}
                onSelect={() => setView({ kind: "dm", userId: u.id })}
              />
            ))}
          </ul>
        </div>
      </aside>

      <section className="sim-chat" aria-label="Conversation">
        {showQuick ? (
          <QuickStart
            server={server}
            welcomeUser={welcomeUser}
            dmTarget={dmTarget}
            onJoin={(u) => void join(u)}
            onDms={(u) => setView({ kind: "dm", userId: u.id })}
            onPreview={() => {
              if (server.ownerId) setActorId(server.ownerId);
              setCommandHint("welcome-preview");
              setMode("command");
            }}
            onClose={() => {
              setShowQuick(false);
              store(QUICKSTART_KEY, "hidden");
            }}
            mock={config.mock}
          />
        ) : null}
        {view?.kind === "dm" ? (
          <DmView
            user={users.find((u) => u.id === view.userId)}
            resolver={resolver}
            botIds={botIds}
            busy={busy}
            onComponent={async (m, id, values) => {
              // Components in a DM are clicked by the DM's recipient.
              const recipient = users.find((u) => u.id === view.userId);
              if (recipient && recipient.id !== actorId) setActorId(recipient.id);
              await onComponent(m, id, values);
            }}
          />
        ) : view?.kind === "channel" ? (
          <ChannelView
            server={server}
            channelId={view.id}
            resolver={resolver}
            botIds={botIds}
            viewer={actor}
            busy={busy}
            onComponent={onComponent}
          />
        ) : (
          <EmptyState title="Pick a channel" />
        )}

        {last ? (
          <InteractionCard
            entry={last}
            resolver={resolver}
            botIds={botIds}
            viewer={actor}
            onClose={() => setLast(null)}
            onOpenModal={() => setModal(last)}
          />
        ) : null}

        <div className="composer">
          <div className="composer-top">
            <div className="composer-tabs" role="tablist" aria-label="Composer mode">
              <button
                role="tab"
                aria-selected={mode === "message"}
                onClick={() => setMode("message")}
              >
                <IconSend size={12} /> Message
              </button>
              <button
                role="tab"
                aria-selected={mode === "command"}
                onClick={() => setMode("command")}
              >
                <IconSlash size={12} /> Slash command
              </button>
            </div>
            <ActorPicker users={users} server={server} value={actor?.id} onChange={setActorId} />
          </div>
          {mode === "message" ? (
            <MessageComposer
              disabled={view?.kind !== "channel" || !actorIsMember}
              reason={
                view?.kind === "dm"
                  ? "DMs are read-only: the simulator records what bots send to users."
                  : !actorIsMember
                    ? `${actor?.displayName || actor?.username || "This user"} is not a member: join the server first.`
                    : undefined
              }
              channelName={
                view?.kind === "channel"
                  ? server.channels.find((c) => c.id === view.id)?.name
                  : undefined
              }
              onSend={async (content) => {
                if (!actor || view?.kind !== "channel") return;
                try {
                  await client.sim.sendMessage({ userId: actor.id, channelId: view.id, content });
                  refresh();
                } catch (e) {
                  toast.push({ tone: "bad", title: "Message not sent", text: errorMessage(e) });
                  throw e;
                }
              }}
            />
          ) : (
            <CommandPanel
              server={server}
              disabled={!actorIsMember}
              reason={
                !actorIsMember ? "Join the server with this user to run commands." : undefined
              }
              onRun={runCommand}
              initial={commandHint}
              channelName={server.channels.find((c) => c.id === channelForCommand)?.name}
            />
          )}
        </div>
      </section>

      <aside className="sim-members" aria-label="Members">
        <MembersPanel
          server={server}
          users={users}
          bots={bots}
          actorId={actor?.id}
          onAct={setActorId}
          onJoin={(u) => void join(u)}
          onLeave={(u) => void leave(u)}
          onDms={(u) => setView({ kind: "dm", userId: u.id })}
          onCreate={async (username) => {
            try {
              const user = await client.sim.createUser(username);
              toast.push({
                tone: "ok",
                title: `Created ${user.username}`,
                text: "Join them to the server to trigger the welcome flow.",
              });
              refresh();
            } catch (e) {
              toast.push({
                tone: "bad",
                title: "Could not create the user",
                text: errorMessage(e),
              });
            }
          }}
        />
      </aside>

      {modal?.result.modal ? (
        <ModalDialog
          modal={modal.result.modal}
          onCancel={() => setModal(null)}
          onSubmit={(fields) => submitModal(modal, modal.result.modal!, fields)}
        />
      ) : null}
    </div>
  );
}

// ───────────────────────────────────────────────────────────── navigation

function ChannelNav({
  server,
  view,
  onSelect,
}: {
  server: SimServer;
  view: View | null;
  onSelect: (v: View) => void;
}) {
  const cats = server.channels.filter((c) => c.type === "category");
  const parent = (c: SimServer["channels"][number]) => (c as { parentId?: string }).parentId ?? "";
  const pos = (c: SimServer["channels"][number]) =>
    Number((c as { position?: number }).position ?? 0);
  const byPos = (a: SimServer["channels"][number], b: SimServer["channels"][number]) =>
    pos(a) - pos(b);
  const loose = server.channels
    .filter((c) => c.type !== "category" && !cats.some((k) => k.id === parent(c)))
    .sort(byPos);
  const item = (c: SimServer["channels"][number]) => {
    const textual = TEXTUAL.has(c.type);
    const active = view?.kind === "channel" && view.id === c.id;
    return (
      <li key={c.id}>
        <button
          className="sim-channel"
          aria-current={active ? "true" : undefined}
          disabled={!textual}
          title={textual ? undefined : "Voice channels are not simulated"}
          onClick={() => onSelect({ kind: "channel", id: c.id })}
        >
          {textual ? <IconHash size={14} /> : <IconVolume size={14} />}
          <span>{c.name}</span>
        </button>
      </li>
    );
  };
  return (
    <nav className="sim-channels" aria-label="Channels">
      <ul>{loose.map(item)}</ul>
      {[...cats].sort(byPos).map((cat) => (
        <div key={cat.id} className="sim-cat">
          <h3 className="sim-cat-title">{cat.name}</h3>
          <ul>
            {server.channels
              .filter((c) => parent(c) === cat.id)
              .sort(byPos)
              .map(item)}
          </ul>
        </div>
      ))}
    </nav>
  );
}

function DmEntry({
  user,
  active,
  onSelect,
}: {
  user: User;
  active: boolean;
  onSelect: () => void;
}) {
  const dms = useSimDms(user.id);
  const count = dms.data?.length ?? 0;
  return (
    <li>
      <button className="sim-channel" aria-current={active ? "true" : undefined} onClick={onSelect}>
        <Avatar name={user.displayName || user.username} url={user.avatarUrl} size={18} />
        <span>{user.displayName || user.username}</span>
        {count ? (
          <span className="sim-count" aria-label={`${count} messages`}>
            {count}
          </span>
        ) : null}
      </button>
    </li>
  );
}

// ───────────────────────────────────────────────────────────── conversation views

function useStickToBottom(dep: unknown) {
  const ref = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  useEffect(() => {
    const el = ref.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [dep]);
  const onScroll = () => {
    const el = ref.current;
    if (el) pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  };
  return { ref, onScroll };
}

function ChannelView({
  server,
  channelId,
  resolver,
  botIds,
  viewer,
  busy,
  onComponent,
}: {
  server: SimServer;
  channelId: string;
  resolver: MentionResolver;
  botIds: ReadonlySet<string>;
  viewer: User | undefined;
  busy: string | null;
  onComponent: (m: Message, customId: string, values: string[]) => Promise<void>;
}) {
  const messages = useSimMessages(channelId);
  const channel = server.channels.find((c) => c.id === channelId);
  const scroll = useStickToBottom(messages.data?.length);
  return (
    <>
      <header className="sim-chat-head">
        <IconHash size={16} />
        <h2>{channel?.name ?? "channel"}</h2>
        {(channel as { topic?: string } | undefined)?.topic ? (
          <span className="muted small">{(channel as { topic?: string }).topic}</span>
        ) : null}
      </header>
      <div className="sim-scroll" ref={scroll.ref} onScroll={scroll.onScroll}>
        {messages.isLoading ? (
          <Skeleton lines={5} />
        ) : !messages.data ? (
          <ErrorState error={messages.error} onRetry={() => void messages.refetch()} />
        ) : messages.data.length === 0 ? (
          <EmptyState
            icon={<IconHash />}
            title={`This is the start of #${channel?.name ?? "channel"}`}
          >
            Send a message, or join a user to see the welcome plugin post here.
          </EmptyState>
        ) : (
          <MessageList
            messages={messages.data}
            resolver={resolver}
            botIds={botIds}
            viewer={viewer}
            busy={busy}
            onComponent={onComponent}
          />
        )}
      </div>
    </>
  );
}

function DmView({
  user,
  resolver,
  botIds,
  busy,
  onComponent,
}: {
  user: User | undefined;
  resolver: MentionResolver;
  botIds: ReadonlySet<string>;
  busy: string | null;
  onComponent: (m: Message, customId: string, values: string[]) => Promise<void>;
}) {
  const dms = useSimDms(user?.id);
  const scroll = useStickToBottom(dms.data?.length);
  const name = user ? user.displayName || user.username : "user";
  return (
    <>
      <header className="sim-chat-head">
        <IconMail size={16} />
        <h2>DMs of {name}</h2>
        <span className="muted small">Messages bots sent to {name}. Links open in a new tab.</span>
      </header>
      <div className="sim-scroll" ref={scroll.ref} onScroll={scroll.onScroll}>
        {dms.isLoading ? (
          <Skeleton lines={4} />
        ) : !dms.data ? (
          <ErrorState error={dms.error} onRetry={() => void dms.refetch()} />
        ) : dms.data.length === 0 ? (
          <EmptyState icon={<IconMail />} title={`No DMs for ${name} yet`}>
            The welcome plugin DMs an onboarding link when a member joins.
          </EmptyState>
        ) : (
          <MessageList
            messages={dms.data}
            resolver={resolver}
            botIds={botIds}
            viewer={user}
            busy={busy}
            onComponent={onComponent}
          />
        )}
      </div>
    </>
  );
}

// ───────────────────────────────────────────────────────────── quick start

function QuickStart({
  server,
  welcomeUser,
  dmTarget,
  onJoin,
  onDms,
  onPreview,
  onClose,
  mock,
}: {
  server: SimServer;
  welcomeUser: User | undefined;
  dmTarget: User | undefined;
  onJoin: (u: User) => void;
  onDms: (u: User) => void;
  onPreview: () => void;
  onClose: () => void;
  mock: boolean;
}) {
  const steps: { title: string; text: ReactNode; action?: ReactNode }[] = [
    {
      title: "Join a user",
      text: <>The welcome plugin greets them in the welcome channel and gives them a role.</>,
      action: welcomeUser ? (
        <Button size="sm" variant="primary" onClick={() => onJoin(welcomeUser)}>
          Join {welcomeUser.displayName || welcomeUser.username}
        </Button>
      ) : (
        <span className="muted small">Everyone has joined: create a user on the right.</span>
      ),
    },
    {
      title: "Open their DMs",
      text: <>The bot sends a link to the onboarding page of the portal.</>,
      action: dmTarget ? (
        <Button size="sm" onClick={() => onDms(dmTarget)}>
          DMs of {dmTarget.displayName || dmTarget.username}
        </Button>
      ) : null,
    },
    {
      title: "Run a command",
      text: <>Try /welcome-preview as the owner of {server.name}.</>,
      action: (
        <Button size="sm" onClick={onPreview}>
          Prepare /welcome-preview
        </Button>
      ),
    },
  ];
  return (
    <div className="quick" role="note" aria-label="Quick start">
      <div className="quick-head">
        <strong>See your bot work in 30 seconds</strong>
        {mock ? <Badge tone="violet">Mock runtime</Badge> : null}
        <button className="icon-btn" aria-label="Hide quick start" onClick={onClose}>
          <IconX size={12} />
        </button>
      </div>
      <ol className="quick-steps">
        {steps.map((s, i) => (
          <li key={s.title}>
            <span className="quick-n">{i + 1}</span>
            <div>
              <p className="quick-title">{s.title}</p>
              <p className="quick-text">{s.text}</p>
              {s.action ? <div className="quick-action">{s.action}</div> : null}
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
}

// ───────────────────────────────────────────────────────────── composer

function ActorPicker({
  users,
  server,
  value,
  onChange,
}: {
  users: User[];
  server: AdminSimServer;
  value: string | undefined;
  onChange: (id: string) => void;
}) {
  const options: PickerOption[] = users
    .map((u) => {
      const member = server.members.some((m) => m.user.id === u.id);
      return {
        value: u.id,
        label: u.displayName || u.username,
        hint: u.id === server.ownerId ? "owner" : member ? "member" : "not a member",
        group: member ? "Members" : "Not in the server",
        icon: <Avatar name={u.displayName || u.username} url={u.avatarUrl} size={16} />,
      };
    })
    .sort((a, b) => (a.group === b.group ? 0 : a.group === "Members" ? -1 : 1));
  return (
    <div className="actor">
      <span className="muted small" id="actor-label">
        Acting as
      </span>
      <Picker
        value={value}
        options={options}
        onChange={(v) => v && onChange(v)}
        aria-labelledby="actor-label"
      />
    </div>
  );
}

function MessageComposer({
  disabled,
  reason,
  channelName,
  onSend,
}: {
  disabled: boolean;
  reason: string | undefined;
  channelName: string | undefined;
  onSend: (content: string) => Promise<void>;
}) {
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const submit = async (e?: FormEvent) => {
    e?.preventDefault();
    const content = text.trim();
    if (!content || disabled) return;
    setSending(true);
    try {
      await onSend(content);
      setText("");
    } catch {
      // toast already shown
    } finally {
      setSending(false);
    }
  };
  return (
    <form className="composer-form" onSubmit={(e) => void submit(e)}>
      <textarea
        className="input composer-input"
        rows={1}
        aria-label="Message"
        placeholder={reason ?? `Message #${channelName ?? "channel"}`}
        disabled={disabled}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            void submit();
          }
        }}
      />
      <Button
        type="submit"
        variant="primary"
        icon={sending ? <Spinner size={12} /> : <IconSend size={14} />}
        disabled={disabled || !text.trim() || sending}
        aria-label="Send message"
      />
    </form>
  );
}

function CommandPanel({
  server,
  disabled,
  reason,
  onRun,
  initial,
  channelName,
}: {
  server: SimServer;
  disabled: boolean;
  reason: string | undefined;
  onRun: (c: SimCommand, o: { [key: string]: Json }) => Promise<void>;
  initial: string | undefined;
  channelName: string | undefined;
}) {
  const commands = useSimCommands(server.id);
  if (commands.isLoading) return <Skeleton lines={2} />;
  if (!commands.data)
    return <ErrorState error={commands.error} onRetry={() => void commands.refetch()} />;
  return (
    <CommandRunner
      key={initial ?? "default"}
      commands={commands.data}
      server={server}
      disabled={disabled}
      disabledReason={reason ?? (channelName ? `Runs in #${channelName}` : undefined)}
      onRun={onRun}
      {...(initial ? { initial } : {})}
    />
  );
}

// ───────────────────────────────────────────────────────────── interaction result

const STATUS_TONE: Record<string, "ok" | "info" | "bad" | "violet"> = {
  replied: "ok",
  deferred: "info",
  failed: "bad",
  modal: "violet",
};

function InteractionCard({
  entry,
  resolver,
  botIds,
  viewer,
  onClose,
  onOpenModal,
}: {
  entry: LastInteraction;
  resolver: MentionResolver;
  botIds: ReadonlySet<string>;
  viewer: User | undefined;
  onClose: () => void;
  onOpenModal: () => void;
}) {
  const r = entry.result;
  return (
    <div
      className="interaction"
      data-status={r.status}
      role="status"
      aria-label="Interaction result"
    >
      <div className="interaction-head">
        <code className="interaction-label">{entry.label}</code>
        <Badge tone={STATUS_TONE[r.status] ?? "neutral"} dot>
          {r.status}
        </Badge>
        {r.response?.ephemeral ? <Badge>ephemeral</Badge> : null}
        {r.followups.length ? (
          <Badge tone="info">
            {r.followups.length} followup{r.followups.length > 1 ? "s" : ""}
          </Badge>
        ) : null}
        <span className="interaction-spacer" />
        <button className="icon-btn" aria-label="Close interaction result" onClick={onClose}>
          <IconX size={12} />
        </button>
      </div>
      {r.error ? <p className="interaction-error">{r.error}</p> : null}
      {r.status === "deferred" && !r.response ? (
        <p className="muted small">
          The bot acknowledged the interaction; its answer will arrive as an edit or followup.
        </p>
      ) : null}
      {r.status === "modal" && r.modal ? (
        <p className="small">
          The bot opened a modal: <strong>{r.modal.title}</strong>{" "}
          <button className="link-btn" onClick={onOpenModal}>
            Fill it in
          </button>
        </p>
      ) : null}
      {r.response || r.followups.length ? (
        <ol className="msg-list interaction-msgs">
          {r.response ? (
            <MessageItem message={r.response} resolver={resolver} botIds={botIds} viewer={viewer} />
          ) : null}
          {r.followups.map((m) => (
            <MessageItem
              key={m.id}
              message={m}
              resolver={resolver}
              botIds={botIds}
              viewer={viewer}
            />
          ))}
        </ol>
      ) : null}
    </div>
  );
}

function ModalDialog({
  modal,
  onCancel,
  onSubmit,
}: {
  modal: SimModal;
  onCancel: () => void;
  onSubmit: (fields: Record<string, string>) => Promise<void>;
}) {
  const inputs = modal.components.flatMap((row) => row.components ?? []);
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(inputs.map((i) => [i.customId, i.value ?? ""])),
  );
  const [sending, setSending] = useState(false);
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open && typeof d.showModal === "function") d.showModal();
  }, []);
  return (
    <dialog ref={ref} className="modal" onCancel={onCancel} aria-labelledby="sim-modal-title">
      <form
        method="dialog"
        onSubmit={(e) => {
          e.preventDefault();
          setSending(true);
          void onSubmit(values).finally(() => setSending(false));
        }}
      >
        <h2 id="sim-modal-title">{modal.title}</h2>
        {inputs.map((i) => (
          <label key={i.customId} className="modal-field">
            <span>
              {i.label}
              {i.required ? <span className="req"> *</span> : null}
            </span>
            {i.style === "paragraph" ? (
              <textarea
                className="input"
                rows={4}
                required={i.required}
                placeholder={i.placeholder}
                value={values[i.customId] ?? ""}
                onChange={(e) => setValues((v) => ({ ...v, [i.customId]: e.target.value }))}
              />
            ) : (
              <input
                className="input"
                required={i.required}
                placeholder={i.placeholder}
                value={values[i.customId] ?? ""}
                onChange={(e) => setValues((v) => ({ ...v, [i.customId]: e.target.value }))}
              />
            )}
          </label>
        ))}
        <div className="modal-actions">
          <Button variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={sending}>
            Submit
          </Button>
        </div>
      </form>
    </dialog>
  );
}

// ───────────────────────────────────────────────────────────── members

function topRoleColor(server: SimServer, roleIds: string[]): string | undefined {
  const roles = server.roles
    .filter((r) => roleIds.includes(r.id) && r.color && r.color !== "#000000")
    .sort(
      (a, b) =>
        Number((b as { position?: number }).position ?? 0) -
        Number((a as { position?: number }).position ?? 0),
    );
  return roles[0]?.color;
}

function MembersPanel({
  server,
  users,
  bots,
  actorId,
  onAct,
  onJoin,
  onLeave,
  onDms,
  onCreate,
}: {
  server: AdminSimServer;
  users: User[];
  bots: User[];
  actorId: string | undefined;
  onAct: (id: string) => void;
  onJoin: (u: User) => void;
  onLeave: (u: User) => void;
  onDms: (u: User) => void;
  onCreate: (username: string) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);
  const botIds = new Set(bots.map((b) => b.id));
  const members = server.members;
  const outside = users.filter((u) => !members.some((m) => m.user.id === u.id));
  return (
    <div className="members">
      <h3 className="sim-nav-title">
        Members <span className="muted">{members.length}</span>
      </h3>
      <ul className="member-list">
        {members.map((m) => {
          const u = m.user;
          const isBot = botIds.has(u.id) || (u as User & { bot?: boolean }).bot;
          const color = topRoleColor(server, m.roles);
          const roleChips = server.roles.filter(
            (r) => m.roles.includes(r.id) && r.id !== server.id && r.name !== "@everyone",
          );
          return (
            <li key={u.id} className="member" data-actor={u.id === actorId || undefined}>
              <Avatar
                name={u.displayName || u.username}
                url={u.avatarUrl}
                size={28}
                bot={!!isBot}
              />
              <div className="member-main">
                <span className="member-name" style={color ? { color } : undefined}>
                  {m.nick || u.displayName || u.username}
                  {u.id === server.ownerId ? (
                    <span className="member-tag" title="Server owner">
                      owner
                    </span>
                  ) : null}
                  {isBot ? <span className="msg-bot-tag">BOT</span> : null}
                </span>
                {roleChips.length ? (
                  <span className="member-roles">
                    {roleChips.slice(0, 4).map((r) => (
                      <span key={r.id} className="member-role">
                        <Swatch color={r.color ?? ""} />
                        {r.name}
                      </span>
                    ))}
                    {roleChips.length > 4 ? (
                      <span className="muted small">+{roleChips.length - 4}</span>
                    ) : null}
                  </span>
                ) : null}
              </div>
              {isBot ? null : (
                <div className="member-actions">
                  <button
                    className="icon-btn"
                    title={`Act as ${u.username}`}
                    aria-label={`Act as ${u.username}`}
                    aria-pressed={u.id === actorId}
                    onClick={() => onAct(u.id)}
                  >
                    <IconUser size={13} />
                  </button>
                  <button
                    className="icon-btn"
                    title={`DMs of ${u.username}`}
                    aria-label={`DMs of ${u.username}`}
                    onClick={() => onDms(u)}
                  >
                    <IconMail size={13} />
                  </button>
                  {u.id !== server.ownerId ? (
                    <button
                      className="icon-btn icon-btn-danger"
                      title={`${u.username} leaves`}
                      aria-label={`${u.username} leaves the server`}
                      onClick={() => onLeave(u)}
                    >
                      <IconLeave size={13} />
                    </button>
                  ) : null}
                </div>
              )}
            </li>
          );
        })}
      </ul>
      <h3 className="sim-nav-title">
        Not in the server <span className="muted">{outside.length}</span>
      </h3>
      {outside.length === 0 ? (
        <p className="muted small">Everyone is in. Create a user below.</p>
      ) : null}
      <ul className="member-list">
        {outside.map((u) => (
          <li key={u.id} className="member member-out">
            <Avatar name={u.displayName || u.username} url={u.avatarUrl} size={28} />
            <div className="member-main">
              <span className="member-name">{u.displayName || u.username}</span>
              <span className="muted small">@{u.username}</span>
            </div>
            <Button
              size="sm"
              variant="primary"
              onClick={() => onJoin(u)}
              aria-label={`${u.username} joins the server`}
            >
              Join
            </Button>
          </li>
        ))}
      </ul>
      <form
        className="create-user"
        onSubmit={(e) => {
          e.preventDefault();
          const v = name.trim();
          if (!v) return;
          setCreating(true);
          void onCreate(v).finally(() => {
            setCreating(false);
            setName("");
          });
        }}
      >
        <label htmlFor="sim-new-user" className="sr-only">
          New user name
        </label>
        <input
          id="sim-new-user"
          className="input"
          placeholder="New user, e.g. dave"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Button
          type="submit"
          size="sm"
          icon={<IconPlus size={12} />}
          loading={creating}
          disabled={!name.trim()}
        >
          Create
        </Button>
      </form>
    </div>
  );
}
