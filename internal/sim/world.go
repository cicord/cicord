package sim

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cicord/cicord/internal/discord"
)

// DefaultInteractionDeadline is how long an interaction may stay unacknowledged
// before it fails, like Discord's 3 second limit.
const DefaultInteractionDeadline = 3 * time.Second

// discordEpoch is the Discord snowflake epoch (2015-01-01T00:00:00Z) in ms.
const discordEpoch = 1420070400000

// allPermissions is what the owner and administrators have.
const allPermissions = ^discord.Permissions(0)

// msgFlagLoading marks a deferred ("Bot is thinking…") response not yet edited.
const msgFlagLoading discord.MessageFlags = 1 << 7

// DefaultEveryonePermissions are the @everyone permissions of a new server. They match
// Discord's defaults: chat, voice and threads, but no management or moderation bits.
var DefaultEveryonePermissions = discord.MustPermissions(
	"CreateInstantInvite", "AddReactions", "Stream", "ViewChannel", "SendMessages",
	"EmbedLinks", "AttachFiles", "ReadMessageHistory", "MentionEveryone",
	"UseExternalEmojis", "Connect", "Speak", "UseVAD", "ChangeNickname",
	"UseApplicationCommands", "RequestToSpeak", "CreatePublicThreads",
	"CreatePrivateThreads", "UseExternalStickers", "SendMessagesInThreads",
	"UseEmbeddedActivities", "UseSoundboard", "UseExternalSounds",
	"SendVoiceMessages", "SendPolls", "UseExternalApps", "PinMessages",
)

// Option configures a World.
type Option func(*World)

// WithClock sets the clock used for timestamps (CreatedAt, JoinedAt, snowflakes,
// timeouts). Interaction deadlines always use real time.
func WithClock(now func() time.Time) Option {
	return func(w *World) { w.now = now }
}

// WithInteractionDeadline overrides the 3 s interaction acknowledgement deadline.
// Values <= 0 keep the default.
func WithInteractionDeadline(d time.Duration) Option {
	return func(w *World) {
		if d > 0 {
			w.deadline = d
		}
	}
}

// WithHiddenChannels makes ListChannels omit, for bots, the channels they cannot view,
// like Discord's GET /guilds/{id}/channels ("This endpoint omits any channel your bot
// doesn't have permission to view"). By default every channel is listed.
func WithHiddenChannels() Option {
	return func(w *World) { w.hideUnviewable = true }
}

// World is the simulated Discord universe. Create it with NewWorld.
type World struct {
	mu sync.Mutex
	// createJournalDelay: see SetCreateJournalDelay.
	createJournalDelay time.Duration
	now                func() time.Time
	deadline           time.Duration
	// hideUnviewable: ListChannels omits the channels a bot cannot view (WithHiddenChannels).
	hideUnviewable bool
	lastID         uint64

	users        map[string]*userState
	botsByToken  map[string]*botState
	botsByUser   map[string]*botState
	botsByApp    map[string]*botState
	servers      map[string]*serverState
	channels     map[string]*channelState
	messages     map[string]*messageState
	dms          map[string]string                       // dmKey(user, bot) → channel ID
	commands     map[string]map[string][]discord.Command // app → server → commands
	interactions map[string]*interactionState            // by ID
	ixByToken    map[string]*interactionState
	openModals   map[string]*openModal // modalKey(user, app) → modal shown to the user
	sessions     map[*Session]struct{} // opened, not closed
	calls        []Call
	subs         map[int]chan struct{}
	cdn          map[string][]byte          // CDN URL → file (uploaded images, expression files)
	analytics    map[string]*serverActivity // server ID → voice, presences, invites, events (analytics.go)
	subSeq       int

	idleMu   sync.Mutex
	idleCond *sync.Cond
	pending  int // events enqueued and not yet fully handled
}

type userState struct {
	locale      string
	user        discord.User
	dmsDisabled bool
	presence    *discord.Presence
	bannerURL   string
}

type botState struct {
	userID     string
	appID      string
	token      string
	privileged discord.Intents // privileged intents enabled in the developer portal
}

type serverState struct {
	incident    discord.IncidentActions
	audit       []discord.AuditLogEntryEvent
	info        discord.Server
	roles       map[string]*discord.Role
	managedBy   map[string]string // role ID → bot user ID for managed roles
	members     map[string]*discord.Member
	memberOrder []string
	bans        map[string]discord.Ban
	channelIDs  []string // creation order, threads included

	// Discord parity: server-level settings and expressions.
	widget     discord.ServerWidget
	automod    map[string]*discord.AutoModRule
	onboarding discord.Onboarding
	welcome    discord.WelcomeScreen
	emojis     map[string]*discord.Emoji
	stickers   map[string]*discord.Sticker
	sounds     map[string]*discord.SoundboardSound
	pinned     map[string]bool // features added with SetFeatures
}

type channelState struct {
	webhooks map[string]string
	ch       discord.Channel
	msgIDs   []string  // non-ephemeral messages, oldest first
	dmUsers  [2]string // DMs: {user ID, bot user ID}
}

type reaction struct {
	emoji string
	users []string
}

type messageState struct {
	pinned       time.Time
	nonce        string
	votes        map[int]map[string]bool
	expires      time.Time
	msg          discord.Message
	reactions    []*reaction
	ephemeral    bool
	ephemeralFor string // user who can see the ephemeral message
	mentions     Mentions
}

// Call is one mutating call recorded in the call log.
type Call struct {
	Seq      int
	ActorID  string // user ID of the bot (or of the human for Admin*/UserClient calls)
	Method   string // discord.Client method name, e.g. "CreateRole"
	ServerID string
	TargetID string // ID of the resource acted upon, when there is one
	Reason   string
	Err      error // nil when the call succeeded
	At       time.Time
}

// NewWorld creates an empty world.
func NewWorld(opts ...Option) *World {
	w := &World{
		now:          time.Now,
		deadline:     DefaultInteractionDeadline,
		users:        map[string]*userState{},
		botsByToken:  map[string]*botState{},
		botsByUser:   map[string]*botState{},
		botsByApp:    map[string]*botState{},
		servers:      map[string]*serverState{},
		channels:     map[string]*channelState{},
		messages:     map[string]*messageState{},
		dms:          map[string]string{},
		commands:     map[string]map[string][]discord.Command{},
		interactions: map[string]*interactionState{},
		ixByToken:    map[string]*interactionState{},
		openModals:   map[string]*openModal{},
		sessions:     map[*Session]struct{}{},
		subs:         map[int]chan struct{}{},
		cdn:          map[string][]byte{},
	}
	w.idleCond = sync.NewCond(&w.idleMu)
	for _, o := range opts {
		o(w)
	}
	return w
}

// Now returns the world's clock time.
func (w *World) Now() time.Time { return w.now() }

// newID returns a monotonic snowflake (18–19 digits). Caller holds w.mu.
func (w *World) newID() string {
	ms := w.now().UnixMilli() - discordEpoch
	if ms < 100_000_000_000 { // keep IDs realistic (18+ digits) with odd clocks
		ms = 100_000_000_000
	}
	id := uint64(ms) << 22 //nolint:gosec // G115: ms is clamped to >= 100_000_000_000 above, never negative
	if id <= w.lastID {
		id = w.lastID + 1
	}
	w.lastID = id
	return strconv.FormatUint(id, 10)
}

func randomToken(prefix string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// snowLess compares two snowflakes numerically.
func snowLess(a, b string) bool {
	x, errA := strconv.ParseUint(a, 10, 64)
	y, errB := strconv.ParseUint(b, 10, 64)
	if errA != nil || errB != nil {
		return a < b
	}
	return x < y
}

// ───────────────────────────────────────────────────────────── errors

func errf(sentinel error, format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{sentinel}, args...)...)
}

func forbidden(format string, args ...any) error {
	return errf(discord.ErrForbidden, format, args...)
}

func notFound(format string, args ...any) error {
	return errf(discord.ErrNotFound, format, args...)
}

func badRequest(format string, args ...any) error {
	return errf(discord.ErrBadRequest, format, args...)
}

// ───────────────────────────────────────────────────────────── call log

// record appends a call to the log and returns err unchanged. Caller holds w.mu.
func (w *World) record(a actor, method, serverID, targetID, reason string, err error) error {
	w.logCall(a, method, serverID, targetID, reason, err)
	return err
}

// recordOK appends a successful call to the log. Caller holds w.mu.
func (w *World) recordOK(a actor, method, serverID, targetID, reason string) {
	w.recordCall(a, method, serverID, targetID, reason, nil, nil)
}

// simAuditActions is Discord's audit-log entry for the calls whose entry the
// simulator writes here (one entry, on the call's target). It is the simulator's own
// table, from Discord's documentation, not the runtime's journal mapping, so that
// attribution tests compare the runtime with Discord rather than with itself. Calls
// with entries that depend on the previous state (channel and role updates, member
// updates, kicks and bans, message deletions) write them where they are modelled.
var simAuditActions = map[string]int{
	"UpdateServer": 1, "CreateChannel": 10, "DeleteChannel": 12, "SetChannelPositions": 16,
	"CreateRole": 30, "DeleteRole": 32, "SetRolePositions": 33,
	"CreateWebhook": 50,
	"CreateEmoji":   60, "UpdateEmoji": 61, "DeleteEmoji": 62,
	"BulkDeleteMessages": 73,
	"CreateSticker":      90, "UpdateSticker": 91, "DeleteSticker": 92,
	"CreateEvent": 100, "EditEvent": 101, "DeleteEvent": 102,
	"CreateThread":          110,
	"CreateSoundboardSound": 130, "UpdateSoundboardSound": 131, "DeleteSoundboardSound": 132,
	"CreateAutoModRule": 140, "UpdateAutoModRule": 141, "DeleteAutoModRule": 142,
}

// recordCall logs a successful call and journals it the way the Discord adapter
// does: before the request when its effects are known, after Discord's response for
// a creation (whose audit entry Discord may already have delivered). fields and
// overwrites describe the request body like the adapter's (nil: unknown). Caller
// holds w.mu.
func (w *World) recordCall(a actor, method, serverID, targetID, reason string, fields, overwrites []string) {
	var journal *discord.OperationScope
	if a.operationContext != nil {
		journal = discord.Operations(a.operationContext)
		reason = journal.Reason(reason)
	}
	effects := discord.CallEffects(method, targetID, fields, overwrites)
	created := strings.HasPrefix(method, "Create")
	if journal != nil && !created {
		journal.Journal(a.operationContext, method, effects, w.now())
	}
	w.logCall(a, method, serverID, targetID, reason, nil)
	if code, ok := simAuditActions[method]; ok && serverID != "" {
		w.auditWithReason(serverID, a.userID, code, targetID, reason)
	}
	if journal != nil && created {
		at, ctx := w.now(), a.operationContext
		if d := w.createJournalDelay; d > 0 {
			go func() {
				time.Sleep(d)
				journal.Journal(ctx, method, effects, at)
			}()
		} else {
			journal.Journal(ctx, method, effects, at)
		}
	}
}

// SetCreateJournalDelay delays the runtime's journal of creations, as when
// Discord's REST response arrives after the gateway delivered the audit entry.
func (w *World) SetCreateJournalDelay(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.createJournalDelay = d
}

func (w *World) logCall(a actor, method, serverID, targetID, reason string, err error) {
	w.calls = append(w.calls, Call{
		Seq: len(w.calls) + 1, ActorID: a.userID, Method: method, ServerID: serverID,
		TargetID: targetID, Reason: reason, Err: err, At: w.now(),
	})
	w.changed()
}

// Calls returns the mutating calls made by actorID (a bot or human user ID), in
// order, failed ones included. An empty actorID returns every call.
func (w *World) Calls(actorID string) []Call {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []Call
	for _, c := range w.calls {
		if actorID == "" || c.ActorID == actorID {
			out = append(out, c)
		}
	}
	return out
}

// CountCalls counts the successful mutating calls of actorID ("" = anyone) whose
// method is one of methods (none = any method).
func (w *World) CountCalls(actorID string, methods ...string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, c := range w.calls {
		if c.Err != nil || (actorID != "" && c.ActorID != actorID) {
			continue
		}
		if len(methods) == 0 {
			n++
			continue
		}
		for _, m := range methods {
			if c.Method == m {
				n++
				break
			}
		}
	}
	return n
}

// ResetCalls clears the call log.
func (w *World) ResetCalls() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = nil
}

// ───────────────────────────────────────────────────────────── users & bots

// CreateUser creates a human user.
func (w *World) CreateUser(username string) discord.User {
	w.mu.Lock()
	defer w.mu.Unlock()
	u := discord.User{ID: w.newID(), Username: username}
	w.users[u.ID] = &userState{user: u}
	w.changed()
	return u
}

// CreateBot creates a bot application. It returns the bot token (for Connect), the bot
// user and the application ID (distinct from the bot user ID, so code mixing the two up
// is caught). All privileged intents are enabled in the simulated developer portal;
// change that with SetPrivilegedIntents.
func (w *World) CreateBot(username string) (token string, bot discord.User, applicationID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	applicationID = w.newID()
	token, bot = w.createBot(username, applicationID)
	return token, bot, applicationID
}

// CreateBotWithApplicationID is CreateBot with a fixed application ID, e.g. to seed a
// world from repository YAML. It fails with ErrBadRequest if applicationID is not a
// valid snowflake or is already used by any entity. The bot user ID is generated.
func (w *World) CreateBotWithApplicationID(username, applicationID string) (token string, bot discord.User, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.reserveID(applicationID); err != nil {
		return "", discord.User{}, err
	}
	token, bot = w.createBot(username, applicationID)
	return token, bot, nil
}

// createBot registers a bot for applicationID. Caller holds w.mu.
func (w *World) createBot(username, applicationID string) (string, discord.User) {
	bot := discord.User{ID: w.newID(), Username: username, Bot: true}
	token := randomToken("sim.")
	b := &botState{userID: bot.ID, appID: applicationID, token: token, privileged: discord.PrivilegedIntents}
	w.users[bot.ID] = &userState{user: bot}
	w.botsByToken[token] = b
	w.botsByUser[bot.ID] = b
	w.botsByApp[applicationID] = b
	w.changed()
	return token, bot
}

// reserveID validates an externally chosen snowflake, checks no entity uses it, and
// moves the generator past it so generated IDs never collide. Caller holds w.mu.
func (w *World) reserveID(id string) error {
	v, err := strconv.ParseUint(id, 10, 64)
	if err != nil || v == 0 || strconv.FormatUint(v, 10) != id || v>>63 != 0 {
		return badRequest("invalid snowflake %q", id)
	}
	if w.idInUse(id) {
		return badRequest("ID %s is already in use", id)
	}
	if v > w.lastID {
		w.lastID = v
	}
	return nil
}

func (w *World) idInUse(id string) bool {
	if w.users[id] != nil || w.servers[id] != nil || w.channels[id] != nil ||
		w.messages[id] != nil || w.botsByApp[id] != nil || w.interactions[id] != nil {
		return true
	}
	for _, s := range w.servers {
		if s.roles[id] != nil {
			return true
		}
	}
	return false
}

// Users returns the human users, sorted by ID.
func (w *World) Users() []discord.User { return w.listUsers(false) }

// Bots returns the bot users, sorted by ID.
func (w *World) Bots() []discord.User { return w.listUsers(true) }

func (w *World) listUsers(bots bool) []discord.User {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []discord.User
	for _, id := range sortedKeys(w.users) {
		if u := w.users[id].user; u.Bot == bots {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return snowLess(out[i].ID, out[j].ID) })
	return out
}

// ApplicationID returns the application ID of a bot user.
func (w *World) ApplicationID(botUserID string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if b := w.botsByUser[botUserID]; b != nil {
		return b.appID, true
	}
	return "", false
}

// Subscribe returns a channel that receives a value whenever the world changes
// (messages, members, channels, roles, servers, users, interactions, calls).
// Notifications are coalesced and never block the world: a slow reader sees at
// least one pending notification, then should re-read what it displays. Call the
// returned function to unsubscribe; it closes the channel and is idempotent.
func (w *World) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	w.mu.Lock()
	w.subSeq++
	id := w.subSeq
	w.subs[id] = ch
	w.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			w.mu.Lock()
			delete(w.subs, id)
			close(ch)
			w.mu.Unlock()
		})
	}
}

// changed notifies subscribers without blocking. Caller holds w.mu.
func (w *World) changed() {
	for _, ch := range w.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// SetPrivilegedIntents sets which privileged intents (ServerMembers, ServerPresences,
// MessageContent) are enabled for the application in the developer portal. Opening a
// gateway with a privileged intent that is not enabled fails, and without MessageContent
// enabled REST reads return messages with empty content (like Discord).
func (w *World) SetPrivilegedIntents(applicationID string, intents discord.Intents) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.botsByApp[applicationID]
	if b == nil {
		return notFound("unknown application %s", applicationID)
	}
	b.privileged = intents & discord.PrivilegedIntents
	return nil
}

// SetDMsEnabled controls whether bots can DM the user ("Allow direct messages from
// server members"). When disabled, a bot's SendMessage to the user's DM fails with
// ErrForbidden.
func (w *World) SetDMsEnabled(userID string, enabled bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	u := w.users[userID]
	if u == nil {
		return notFound("unknown user %s", userID)
	}
	u.dmsDisabled = !enabled
	w.changed()
	return nil
}

// User returns a user (human or bot).
func (w *World) User(userID string) (discord.User, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	u := w.users[userID]
	if u == nil {
		return discord.User{}, false
	}
	return u.user, true
}

// UserBanner returns the CDN URL of a user's profile banner ("" = none).
func (w *World) UserBanner(userID string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if u := w.users[userID]; u != nil {
		return u.bannerURL
	}
	return ""
}

// Presence returns the presence last set by a bot with SetPresence.
func (w *World) Presence(userID string) (discord.Presence, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	u := w.users[userID]
	if u == nil || u.presence == nil {
		return discord.Presence{}, false
	}
	return *u.presence, true
}

// ───────────────────────────────────────────────────────────── clones

func cloneOverwrites(in []discord.Overwrite) []discord.Overwrite {
	if in == nil {
		return nil
	}
	return append([]discord.Overwrite{}, in...)
}

func cloneChannel(c discord.Channel) discord.Channel {
	c.Overwrites = cloneOverwrites(c.Overwrites)
	if c.Tags != nil {
		c.Tags = append([]discord.ForumTag{}, c.Tags...)
	}
	return c
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func cloneInt(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneFloat(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneMember(m discord.Member) discord.Member {
	if m.RoleIDs != nil {
		m.RoleIDs = append([]string{}, m.RoleIDs...)
	}
	m.PremiumSince = cloneTime(m.PremiumSince)
	if m.Pending != nil {
		v := *m.Pending
		m.Pending = &v
	}
	m.TimeoutUntil = cloneTime(m.TimeoutUntil)
	return m
}

func cloneMemberPtr(m *discord.Member) *discord.Member {
	if m == nil {
		return nil
	}
	c := cloneMember(*m)
	return &c
}

func cloneEmbeds(in []discord.Embed) []discord.Embed {
	if in == nil {
		return nil
	}
	out := make([]discord.Embed, len(in))
	for i, e := range in {
		e.Timestamp = cloneTime(e.Timestamp)
		if e.Fields != nil {
			e.Fields = append([]discord.EmbedField{}, e.Fields...)
		}
		if e.Author != nil {
			a := *e.Author
			e.Author = &a
		}
		if e.Footer != nil {
			f := *e.Footer
			e.Footer = &f
		}
		out[i] = e
	}
	return out
}

func cloneComponents(in []discord.Component) []discord.Component {
	if in == nil {
		return nil
	}
	out := make([]discord.Component, len(in))
	for i, c := range in {
		c.Components = cloneComponents(c.Components)
		if c.Accessory != nil {
			a := cloneComponents([]discord.Component{*c.Accessory})[0]
			c.Accessory = &a
		}
		c.DefaultValues = slices.Clone(c.DefaultValues)
		c.ChannelTypes = slices.Clone(c.ChannelTypes)
		c.Items = slices.Clone(c.Items)
		if c.Options != nil {
			c.Options = append([]discord.SelectOption{}, c.Options...)
		}
		c.MinValues, c.MaxValues = cloneInt(c.MinValues), cloneInt(c.MaxValues)
		c.MinLength, c.MaxLength = cloneInt(c.MinLength), cloneInt(c.MaxLength)
		out[i] = c
	}
	return out
}

// restMessage is a message as a REST call returns it (fetch, list, pins, and the result of a
// send or an edit): Discord sends the author's member only on gateway MESSAGE_CREATE and
// MESSAGE_UPDATE events, never in REST message objects.
func restMessage(m discord.Message) discord.Message {
	out := cloneMessage(m)
	out.Member = nil
	return out
}

func cloneMessage(m discord.Message) discord.Message {
	if m.PollResults != nil {
		p := *m.PollResults
		p.Answers = append([]discord.PollAnswer(nil), p.Answers...)
		m.PollResults = &p
	}
	m.Member = cloneMemberPtr(m.Member)
	m.Embeds = cloneEmbeds(m.Embeds)
	m.Components = cloneComponents(m.Components)
	if m.Attachments != nil {
		m.Attachments = append([]discord.Attachment{}, m.Attachments...)
	}
	m.EditedAt = cloneTime(m.EditedAt)
	if m.Interaction != nil {
		a := *m.Interaction
		m.Interaction = &a
	}
	if m.StickerIDs != nil {
		m.StickerIDs = append([]string{}, m.StickerIDs...)
	}
	return m
}

func cloneMessagePtr(m *discord.Message) *discord.Message {
	if m == nil {
		return nil
	}
	c := cloneMessage(*m)
	return &c
}

func cloneCommandOptions(in []discord.CommandOption) []discord.CommandOption {
	if in == nil {
		return nil
	}
	opts := make([]discord.CommandOption, len(in))
	for i, o := range in {
		if o.Choices != nil {
			o.Choices = append([]discord.CommandChoice{}, o.Choices...)
		}
		o.MinValue, o.MaxValue = cloneFloat(o.MinValue), cloneFloat(o.MaxValue)
		o.MinLength, o.MaxLength = cloneInt(o.MinLength), cloneInt(o.MaxLength)
		o.Options = cloneCommandOptions(o.Options)
		opts[i] = o
	}
	return opts
}

func cloneInteractionOptions(in []discord.InteractionOption) []discord.InteractionOption {
	if in == nil {
		return nil
	}
	out := make([]discord.InteractionOption, len(in))
	for i, o := range in {
		o.Options = cloneInteractionOptions(o.Options)
		out[i] = o
	}
	return out
}

func cloneCommand(c discord.Command) discord.Command {
	c.Options = cloneCommandOptions(c.Options)
	if c.DefaultMemberPermissions != nil {
		p := *c.DefaultMemberPermissions
		c.DefaultMemberPermissions = &p
	}
	return c
}

func cloneModal(m *discord.Modal) *discord.Modal {
	if m == nil {
		return nil
	}
	c := *m
	c.Components = cloneComponents(m.Components)
	return &c
}

func cloneInteraction(ix discord.Interaction) discord.Interaction {
	ix.TargetMessage = cloneMessagePtr(ix.TargetMessage)
	ix.TargetMember = cloneMemberPtr(ix.TargetMember)
	ix.Member = cloneMemberPtr(ix.Member)
	ix.Options = cloneInteractionOptions(ix.Options)
	r := ix.Resolved
	ix.Resolved = discord.Resolved{}
	if r.Users != nil {
		ix.Resolved.Users = map[string]discord.User{}
		for k, v := range r.Users {
			ix.Resolved.Users[k] = v
		}
	}
	if r.Members != nil {
		ix.Resolved.Members = map[string]discord.Member{}
		for k, v := range r.Members {
			ix.Resolved.Members[k] = cloneMember(v)
		}
	}
	if r.Roles != nil {
		ix.Resolved.Roles = map[string]discord.Role{}
		for k, v := range r.Roles {
			ix.Resolved.Roles[k] = v
		}
	}
	if r.Channels != nil {
		ix.Resolved.Channels = map[string]discord.Channel{}
		for k, v := range r.Channels {
			ix.Resolved.Channels[k] = cloneChannel(v)
		}
	}
	if r.Attachments != nil {
		ix.Resolved.Attachments = map[string]discord.Attachment{}
		for k, v := range r.Attachments {
			ix.Resolved.Attachments[k] = v
		}
	}
	if ix.Values != nil {
		ix.Values = append([]string{}, ix.Values...)
	}
	ix.Message = cloneMessagePtr(ix.Message)
	if ix.ModalFields != nil {
		f := make(map[string]string, len(ix.ModalFields))
		for k, v := range ix.ModalFields {
			f[k] = v
		}
		ix.ModalFields = f
	}
	return ix
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
