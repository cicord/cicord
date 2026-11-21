import type { BotStatus, RuntimeStatus, ServerStatus, SyncRecord } from "@codi/client";
import { useHistory, useStatus } from "../api.ts";
import { Link } from "../router.tsx";
import { IconBot, IconBranch, IconCommit, IconPlay, IconServer } from "../ui/icons.tsx";
import {
  Avatar,
  Badge,
  Callout,
  EmptyState,
  ErrorState,
  HealthBadge,
  PageHeader,
  Panel,
  Sha,
  Skeleton,
  SyncBadge,
  Time,
} from "../ui/index.tsx";

export function OverviewPage() {
  const status = useStatus();
  if (status.isLoading)
    return (
      <div className="page">
        <PageHeader title="Overview" />
        <Skeleton lines={6} />
      </div>
    );
  if (!status.data)
    return (
      <div className="page">
        <PageHeader title="Overview" />
        <ErrorState error={status.error} onRetry={() => void status.refetch()} />
      </div>
    );
  const s = status.data;
  return (
    <div className="page">
      <PageHeader
        title="Overview"
        meta={
          <>
            <span>
              <code>codi {s.mode}</code> · v{s.version}
            </span>
            {s.simulator ? (
              <Badge tone="violet" title="Discord is simulated in-process (codi dev --sim)">
                Simulator
              </Badge>
            ) : null}
          </>
        }
        actions={
          s.simulator ? (
            <Link to="/simulator" className="btn btn-primary btn-md">
              <IconPlay size={14} />
              <span>Open simulator</span>
            </Link>
          ) : null
        }
      />
      {s.lastError ? (
        <Callout tone="bad" title="The latest revision was rejected">
          <code className="error-code">{s.lastError}</code>
          <p className="muted">
            The runtime keeps running the last good revision until a fix is pushed.
          </p>
        </Callout>
      ) : null}
      <RevisionHero status={s} />
      <div className="overview-grid">
        <div className="overview-main">
          <Panel title="Servers" description="Reconciled from servers/*.yaml">
            {s.servers.length === 0 ? (
              <EmptyState icon={<IconServer />} title="No servers visible">
                You see the servers where you are an admin or moderator.
              </EmptyState>
            ) : (
              <div className="server-cards">
                {s.servers.map((srv) => (
                  <ServerCard key={srv.key} server={srv} />
                ))}
              </div>
            )}
          </Panel>
          <Panel title="Bots" description="Plugin instances run per bot and server">
            {s.bots.length === 0 ? (
              <EmptyState icon={<IconBot />} title="No bots running" />
            ) : (
              <ul className="bot-list">
                {s.bots.map((b) => (
                  <BotRow key={b.key} bot={b} />
                ))}
              </ul>
            )}
          </Panel>
        </div>
        <aside className="overview-side">
          <HistoryPanel />
        </aside>
      </div>
    </div>
  );
}

function RevisionHero({ status }: { status: RuntimeStatus }) {
  const rev = status.revision;
  if (!rev)
    return (
      <div className="rev-hero rev-hero-empty">
        <IconCommit size={20} />
        <div>
          <p className="rev-message">No active revision yet</p>
          <p className="muted">The runtime is waiting for its first valid revision.</p>
        </div>
      </div>
    );
  return (
    <section className="rev-hero" aria-label="Active revision">
      <div className="rev-sha">
        <span className="rev-label">Active revision</span>
        <code className="rev-id" title={rev.sha ?? rev.id}>
          {rev.id}
        </code>
      </div>
      <div className="rev-body">
        <p className="rev-message">{rev.message.split("\n")[0] || "(no message)"}</p>
        <p className="rev-meta">
          <span>{rev.author || "unknown author"}</span>
          <span aria-hidden="true">·</span>
          <Time iso={rev.time} />
        </p>
      </div>
      <div className="rev-source">
        <IconBranch size={14} />
        <span>{rev.source || "source"}</span>
      </div>
    </section>
  );
}

function ServerCard({ server }: { server: ServerStatus }) {
  const drift = server.drift?.length ?? 0;
  return (
    <article className="server-card" data-sync={server.sync}>
      <header className="server-card-head">
        <div>
          <h3 className="server-card-name">
            <Link to={`/servers/${server.key}`}>{server.name}</Link>
          </h3>
          <code className="muted small">{server.key}</code>
        </div>
        <SyncBadge sync={server.sync} />
      </header>
      <dl className="kv">
        <div>
          <dt>Health</dt>
          <dd>
            <HealthBadge health={server.health} title={server.message} />
          </dd>
        </div>
        <div>
          <dt>Drift</dt>
          <dd>
            {drift === 0 ? (
              <span className="muted">none</span>
            ) : (
              <Link to={`/servers/${server.key}`} className="drift-count">
                {drift} field{drift > 1 ? "s" : ""}
              </Link>
            )}
          </dd>
        </div>
        <div>
          <dt>Last sync</dt>
          <dd>
            {server.lastSync ? (
              <Time iso={server.lastSync} />
            ) : (
              <span className="muted">never</span>
            )}
          </dd>
        </div>
        {server.bot ? (
          <div>
            <dt>Bot</dt>
            <dd>
              <code>{server.bot}</code>
            </dd>
          </div>
        ) : null}
      </dl>
      {server.message ? <p className="server-card-msg">{server.message}</p> : null}
      <footer className="server-card-foot">
        <Link to={`/servers/${server.key}/plan`}>Plan</Link>
        <Link to={`/servers/${server.key}/resources`}>Resources</Link>
        <Link to={`/servers/${server.key}/plugins`}>Plugins</Link>
      </footer>
    </article>
  );
}

function BotRow({ bot }: { bot: BotStatus }) {
  return (
    <li className="bot-row">
      <div className="bot-id">
        <Avatar name={bot.username || bot.key} size={32} bot />
        <div>
          <div className="bot-name">
            {bot.username || bot.key}
            <Badge>bot</Badge>
          </div>
          <div className="muted small">
            <code>{bot.key}</code>
            {bot.servers.length ? ` · ${bot.servers.join(", ")}` : ""}
          </div>
        </div>
        <HealthBadge health={bot.health} title={bot.message} />
      </div>
      {bot.message ? <p className="bot-msg">{bot.message}</p> : null}
      {bot.plugins.length ? (
        <ul className="instance-list">
          {bot.plugins.map((p) => (
            <li key={`${p.server}/${p.instance}`} className="instance">
              <span
                className={`dot dot-${p.health === "Healthy" ? "ok" : p.health === "Degraded" ? "bad" : p.health === "Progressing" ? "info" : "warn"}`}
              />
              <Link
                to={`/servers/${p.server}/plugins#${bot.key}-${p.instance}`}
                className="instance-name"
              >
                {p.instance}
              </Link>
              <span className="muted small">
                {p.plugin !== p.instance ? `${p.plugin} ` : ""}v{p.version}
              </span>
              <code className="muted small instance-server">{p.server}</code>
              {p.message ? <span className="instance-msg">{p.message}</span> : null}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted small">No plugin instances.</p>
      )}
    </li>
  );
}

const STATUS_TONE = (status: string): string => {
  const s = status.toLowerCase();
  if (/(synced|succe|ok|applied|healthy|active)/.test(s)) return "ok";
  if (/(fail|error|reject|degraded)/.test(s)) return "bad";
  if (/(progress|running|pending)/.test(s)) return "info";
  return "neutral";
};

function HistoryPanel() {
  const history = useHistory(30);
  return (
    <Panel title="Sync history" description="Most recent first">
      {history.isLoading ? (
        <Skeleton lines={6} />
      ) : !history.data ? (
        <ErrorState error={history.error} onRetry={() => void history.refetch()} />
      ) : history.data.length === 0 ? (
        <EmptyState title="Nothing synced yet" />
      ) : (
        <ol className="timeline">
          {history.data.map((h: SyncRecord) => (
            <li key={h.id} className="timeline-item" data-tone={STATUS_TONE(h.status)}>
              <span className="timeline-dot" aria-hidden="true" />
              <div className="timeline-head">
                <span className={`timeline-status tone-${STATUS_TONE(h.status)}`}>{h.status}</span>
                <code className="timeline-target">{h.target}</code>
                <Time iso={h.time} />
              </div>
              <p className="timeline-msg">{h.message}</p>
              <Sha id={h.revision} />
            </li>
          ))}
        </ol>
      )}
    </Panel>
  );
}
