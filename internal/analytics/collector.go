package analytics

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// Collector timing and bounds.
const (
	// SampleInterval is how often each server's counts are read from Discord (approximate
	// member and online counts, boosts, scheduled events, invites, presences).
	SampleInterval = 5 * time.Minute
	// FlushInterval is how often buffered points are written to the store.
	FlushInterval = 30 * time.Second
	// InviteDebounce groups the joins of a burst into one invite list fetch.
	InviteDebounce = 2 * time.Second
	// BotsRefresh is how often the member list is read again to know the bot members
	// (join and leave events keep it current in between).
	BotsRefresh = 24 * time.Hour

	// maxDimsPerFlush caps the dimensions of a metric per server and hour in one flush;
	// the smallest ones are summed into "other".
	maxDimsPerFlush = 100
	// maxBufferedPoints bounds the buffer when the store keeps failing.
	maxBufferedPoints = 100_000
	// maxPendingActive bounds the members waiting to be marked active.
	maxPendingActive = 200_000
	// topActivities is how many presence activities are sampled per server.
	topActivities = 20
	// autoModWindow groups the actions of one AutoMod trigger (one event per action).
	autoModWindow = 5 * time.Second
	// maxRollupDays is how far back a missed daily rollup is caught up.
	maxRollupDays = 7
	// dayKeyTTL keeps the daily HMAC key across a restart within the day.
	dayKeyTTL = 48 * time.Hour
)

// OtherDim holds the dimensions beyond maxDimsPerFlush.
const OtherDim = "other"

// CollectorIntents are the non-privileged gateway intents the collecting bot of a
// server needs. They are added to its identify intents when analytics are enabled.
// The privileged ServerMembers and ServerPresences intents are never added: they are
// used only when the bot already requests them.
var CollectorIntents = discord.MustIntents("ServerVoiceStates", "ServerInvites", "ServerModeration",
	"AutoModerationExecution", "ServerMessageReactions", "ServerMessages")

var (
	intentMembers        = discord.MustIntents("ServerMembers")
	intentPresences      = discord.MustIntents("ServerPresences")
	intentMessageContent = discord.MustIntents("MessageContent")
	permViewAuditLog     = discord.MustPermissions("ViewAuditLog")
	permManageServer     = discord.MustPermissions("ManageServer")
	permAdministrator    = discord.MustPermissions("Administrator")
)

// CollectorServer describes a server for the collector.
type CollectorServer struct {
	Key string // server key (state.AnalyticsPoint.Server)
	ID  string // Discord server ID

	// spec.analytics
	Enabled       bool
	RetentionDays int
	Presence      bool

	// The collecting bot: the first bot key, in sorted order, that lists the server.
	Bot           string // "" when no bot lists the server
	BotFile       string // its bot.yaml, for the fix messages
	ApplicationID string
	Intents       discord.Intents // the intents it identifies with
	Connected     bool
	BotUserID     string
	// Client is the bot's REST client (nil when not connected); it may implement
	// discord.AnalyticsReader.
	Client discord.Client

	// Live lookups (runtime cache); nil when unknown.
	Channel      func(id string) (discord.Channel, bool)
	AFKChannelID func() string
	// Permissions returns the bot's server-level permissions.
	Permissions func(ctx context.Context) (discord.Permissions, error)
}

// isBot tells whether a user is a bot: one of the runtime's bots or a bot member of the
// server. Caller holds c.mu.
func (c *Collector) isBot(s *collected, userID string) bool {
	return c.botUsers[userID] || s.bots[userID]
}

func (s CollectorServer) presenceCollected() bool {
	return s.Presence && s.Connected && s.Intents.Has(intentPresences)
}

// CollectorOptions configures a Collector.
type CollectorOptions struct {
	Store state.Analytics
	// KV is the "analytics" scope: daily HMAC keys and the rollup marker.
	KV     state.KV
	Logger *slog.Logger
	Now    func() time.Time
	// Intervals (0 = the defaults above).
	FlushInterval  time.Duration
	SampleInterval time.Duration
	InviteDebounce time.Duration
}

// Collector turns gateway events and periodic samples into hourly aggregates. It
// keeps counters in memory and writes them every FlushInterval; Handle never does I/O.
// It is safe for concurrent use.
type Collector struct {
	opts CollectorOptions
	log  *slog.Logger

	mu       sync.Mutex
	servers  map[string]*collected // by Discord ID
	byKey    map[string]*collected
	botUsers map[string]bool // user IDs of the runtime's bots (their audit entries are GitOps)
	// snapshots keeps the last ServerAvailableEvent of every bot and server, replayed
	// when a server gets its collecting bot (the gateway connects before Configure).
	snapshots map[string]discord.ServerAvailableEvent
	buf       buffer
	pending   map[activeKey]map[string]struct{} // members to mark active (user IDs, memory only)
	npending  int
	stopped   bool
	wg        sync.WaitGroup // invite refreshes

	// Guarded by tickMu (flush, samples, daily work run one at a time).
	tickMu    sync.Mutex
	seen      map[activeKey]map[string]struct{} // tokens already marked
	dayKeys   map[string][]byte
	rolledUp  string            // last day rolled up in this process ("" = check the store)
	purged    map[string]string // server key → day of the last purge
	lastFlush time.Time
}

// collected is the in-memory state of one server.
type collected struct {
	cfg CollectorServer

	voice    map[string]*voiceSession // user ID → session
	presence map[string]presenceInfo  // user ID → presence (when collected)

	// bots are the user IDs of the server's bot members, read from the member list
	// (needs ServerMembers) at botsAt and kept current by join and leave events; nil
	// until read.
	bots   map[string]bool
	botsAt time.Time

	invMu   sync.Mutex // one invite fetch at a time
	invites inviteState

	permsKnown       bool
	perms            discord.Permissions
	invitesForbidden bool
	lastSample       time.Time
	autoMod          map[string]time.Time // rule|user|channel → last trigger
}

// NewCollector returns a collector; call Configure, then Run.
func NewCollector(opts CollectorOptions) *Collector {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = FlushInterval
	}
	if opts.SampleInterval <= 0 {
		opts.SampleInterval = SampleInterval
	}
	if opts.InviteDebounce <= 0 {
		opts.InviteDebounce = InviteDebounce
	}
	return &Collector{
		opts: opts, log: opts.Logger.With("component", "analytics"),
		servers: map[string]*collected{}, byKey: map[string]*collected{}, botUsers: map[string]bool{},
		snapshots: map[string]discord.ServerAvailableEvent{}, buf: newBuffer(),
		pending: map[activeKey]map[string]struct{}{}, seen: map[activeKey]map[string]struct{}{},
		dayKeys: map[string][]byte{}, purged: map[string]string{},
	}
}

func (c *Collector) now() time.Time { return c.opts.Now().UTC() }

// Configure sets the servers to collect and the user IDs of the runtime's bots. The
// state of servers that stay (same key, same collecting bot) is kept.
func (c *Collector) Configure(servers []CollectorServer, botUserIDs []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.botUsers = map[string]bool{}
	for _, id := range botUserIDs {
		c.botUsers[id] = true
	}
	next := make(map[string]*collected, len(servers))
	byKey := make(map[string]*collected, len(servers))
	for _, cfg := range servers {
		if cfg.ID == "" || cfg.Key == "" {
			continue
		}
		s := c.servers[cfg.ID]
		fresh := s == nil || s.cfg.Key != cfg.Key || s.cfg.Bot != cfg.Bot || !s.cfg.Enabled
		if s != nil && fresh {
			c.endVoice(s, now)
		}
		if s == nil || s.cfg.Key != cfg.Key {
			s = &collected{autoMod: map[string]time.Time{}}
		}
		if fresh {
			s.voice, s.presence = map[string]*voiceSession{}, map[string]presenceInfo{}
			s.bots, s.botsAt = nil, time.Time{}
		}
		s.cfg = cfg
		if !cfg.Enabled {
			s.voice, s.presence = map[string]*voiceSession{}, map[string]presenceInfo{}
		}
		if !cfg.presenceCollected() {
			s.presence = map[string]presenceInfo{}
		}
		next[cfg.ID], byKey[cfg.Key] = s, s
		if fresh && cfg.Enabled {
			if snap, ok := c.snapshots[cfg.Bot+"/"+cfg.ID]; ok {
				c.applySnapshot(s, snap, now)
			}
		}
	}
	for id, s := range c.servers {
		if next[id] == nil {
			c.endVoice(s, now)
		}
	}
	c.servers, c.byKey = next, byKey
}

// Handle records a gateway event received by a bot. It never blocks on I/O.
func (c *Collector) Handle(bot string, ev discord.Event) {
	sid := discord.ServerIDOf(ev)
	if sid == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if snap, ok := ev.(discord.ServerAvailableEvent); ok {
		c.snapshots[bot+"/"+sid] = snap
	}
	s := c.servers[sid]
	if s == nil || !s.cfg.Enabled || c.stopped {
		return
	}
	now := c.now()
	// Interactions only reach the application they belong to: every bot counts its own.
	if e, ok := ev.(discord.InteractionCreateEvent); ok {
		c.interaction(s, e.Interaction, now)
		return
	}
	// Every other event reaches every bot of the server: only the collecting bot counts.
	if bot != s.cfg.Bot {
		return
	}
	c.event(s, ev, now)
}

// Run flushes, samples and does the daily work until ctx ends, then flushes a last
// time (bounded to 10 s).
func (c *Collector) Run(ctx context.Context) {
	c.Tick(ctx)
	t := time.NewTicker(c.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			c.Stop(stopCtx)
			cancel()
			return
		case <-t.C:
			c.Tick(ctx)
		}
	}
}

// Tick samples the servers that are due, flushes the buffer, then rolls up the
// previous days and applies retention once a day.
func (c *Collector) Tick(ctx context.Context) {
	c.sampleDue(ctx)
	c.Flush(ctx)
	c.daily(ctx)
}

// Stop cancels pending invite fetches, waits for running ones and flushes.
func (c *Collector) Stop(ctx context.Context) {
	c.mu.Lock()
	c.stopped = true
	for _, s := range c.servers {
		if s.invites.timer != nil && s.invites.timer.Stop() {
			s.invites.timer = nil
			c.wg.Done()
		}
	}
	c.mu.Unlock()
	c.wg.Wait()
	c.Flush(ctx)
}

// Flush credits the time of open voice sessions, then writes the buffered points (one
// Record call per kind) and marks the pending active members. A failing store keeps
// the points for the next flush (bounded).
func (c *Collector) Flush(ctx context.Context) {
	c.tickMu.Lock()
	defer c.tickMu.Unlock()
	c.mu.Lock()
	now := c.now()
	day := dayOf(now)
	for _, s := range c.servers {
		if !s.cfg.Enabled {
			continue
		}
		for uid, vs := range s.voice {
			c.credit(s, vs, now)
			if vs.day != day { // still in voice on a new day
				vs.day = day
				c.markActive(s, uid, now, ActiveSetVoice, ActiveSetAll)
			}
		}
		c.notePeak(s, now)
	}
	batches := c.buf.take()
	pending := c.pending
	c.pending, c.npending = map[activeKey]map[string]struct{}{}, 0
	c.lastFlush = now
	c.mu.Unlock()

	if c.opts.Store == nil {
		return
	}
	for _, kind := range []state.AnalyticsKind{state.AnalyticsCounter, state.AnalyticsGaugeMax, state.AnalyticsGaugeLast} {
		pts := batches[kind]
		if len(pts) == 0 {
			continue
		}
		if err := c.opts.Store.Record(ctx, kind, pts...); err != nil {
			c.log.Warn("analytics: record points", "kind", kind, "points", len(pts), "err", err)
			c.mu.Lock()
			c.buf.restore(kind, pts)
			c.mu.Unlock()
		}
	}
	c.flushActive(ctx, pending)
}

// sampleDue samples every enabled, connected server whose last sample is older than
// SampleInterval.
func (c *Collector) sampleDue(ctx context.Context) {
	c.mu.Lock()
	now := c.now()
	var due []*collected
	for _, s := range c.servers {
		if s.cfg.Enabled && s.cfg.Connected && now.Sub(s.lastSample) >= c.opts.SampleInterval {
			s.lastSample = now
			due = append(due, s)
		}
	}
	c.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].cfg.Key < due[j].cfg.Key })
	for _, s := range due {
		if ctx.Err() != nil {
			return
		}
		c.Sample(ctx, s.cfg.Key)
	}
}

// colDimensioned holds the catalog metrics that have a dimension: they are written
// per dimension only, never with a "" total (the reader computes totals).
var colDimensioned = func() map[string]bool {
	m := map[string]bool{}
	for _, x := range Catalog {
		if x.Dim != "" {
			m[x.Name] = true
		}
	}
	return m
}()

// count adds to a counter of a server. Caller holds c.mu.
func (c *Collector) count(s *collected, metric, dim string, at time.Time, v float64) {
	if v == 0 || dim == "" && colDimensioned[metric] {
		return
	}
	if !c.buf.add(state.AnalyticsCounter, s.cfg.Key, metric, dim, at, v) {
		c.log.Warn("analytics: buffer full, dropping points", "server", s.cfg.Key, "metric", metric)
	}
}

// gauge records a gauge of a server. Caller holds c.mu.
func (c *Collector) gauge(s *collected, kind state.AnalyticsKind, metric, dim string, at time.Time, v float64) {
	if dim == "" && colDimensioned[metric] {
		return
	}
	c.buf.add(kind, s.cfg.Key, metric, dim, at, v)
}

func hourOf(t time.Time) time.Time { return t.UTC().Truncate(time.Hour) }

func dayOf(t time.Time) string { return t.UTC().Format(time.DateOnly) }
