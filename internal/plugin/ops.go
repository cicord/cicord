package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/state"
)

// Capabilities (https://cicord.dev/docs/plugins/writing/).
const (
	capMessagesRead    = "discord.messages.read"
	capMessagesSend    = "discord.messages.send"
	capMessagesManage  = "discord.messages.manage"
	capReactions       = "discord.reactions.manage"
	capMembersRead     = "discord.members.read"
	capMembersManage   = "discord.members.manage"
	capInvitesRead     = "discord.invites.read"
	capAuditRead       = "discord.auditlog.read"
	capMembersModerate = "discord.members.moderate"
	capRolesAssign     = "discord.roles.assign"
	capChannelsRead    = "discord.channels.read"
	capChannelsManage  = "discord.channels.manage"
	capThreadsManage   = "discord.threads.manage"
	capVoiceMove       = "discord.voice.move"
	capDMSend          = "discord.dm.send"
	capKV              = "storage.kv"
	capScheduler       = "scheduler"
	capHTTPFetch       = "http.fetch"
	capPortalRoutes    = "portal.routes"
	capPortalLinks     = "portal.links"
	capEventsPublish   = "events.publish"
	capEventsSubscribe = "events.subscribe"
)

// capabilityPermissions are the Discord permissions the bot needs for the calls each
// capability allows (when it is not Administrator). Capabilities missing here need none
// (members.read and dm.send use the gateway member list and DMs).
var capabilityPermissions = map[string]discord.Permissions{
	capMessagesRead:           discord.MustPermissions("ViewChannel", "ReadMessageHistory"),
	capMessagesSend:           discord.MustPermissions("ViewChannel", "SendMessages", "EmbedLinks"),
	capMessagesManage:         discord.MustPermissions("ViewChannel", "ManageMessages", "PinMessages"),
	"discord.webhooks.manage": discord.MustPermissions("ViewChannel", "ManageWebhooks"),
	capReactions:              discord.MustPermissions("ViewChannel", "AddReactions", "ReadMessageHistory"),
	capMembersManage:          discord.MustPermissions("ManageNicknames"),
	capInvitesRead:            discord.MustPermissions("ManageServer"),
	capAuditRead:              discord.MustPermissions("ViewAuditLog"),
	capMembersModerate:        discord.MustPermissions("ModerateMembers", "KickMembers", "BanMembers"),
	capRolesAssign:            discord.MustPermissions("ManageRoles"),
	capChannelsRead:           discord.MustPermissions("ViewChannel"),
	capChannelsManage:         discord.MustPermissions("ViewChannel", "ManageChannels", "ManageRoles"),
	"server.incident":         discord.MustPermissions("ManageServer"),
	capThreadsManage: discord.MustPermissions("ViewChannel", "CreatePublicThreads", "CreatePrivateThreads",
		"ManageThreads", "SendMessagesInThreads"),
	// Moving a member needs MoveMembers, and Connect in the destination channel.
	capVoiceMove: discord.MustPermissions("ViewChannel", "Connect", "MoveMembers"),
}

// CapabilityPermissions returns the Discord permissions the bot needs for a granted
// capability (0 when it needs none).
func CapabilityPermissions(capability string) discord.Permissions {
	return capabilityPermissions[capability]
}

func (i *Instance) require(capability string) error {
	if !i.grants[capability] {
		return &CapabilityError{Capability: capability}
	}
	return nil
}

// argError is an invalid argument; it surfaces as a TypeError in JS.
type argError struct{ msg string }

func (e *argError) Error() string        { return e.msg }
func (e *argError) Is(target error) bool { return target == errInvalidArgument }

func argErr(format string, args ...any) error { return &argError{msg: fmt.Sprintf(format, args...)} }

// ───────────────────────────────────────────────────────────── host interface

type syncOp func(i *Instance, inv *invocation, args json.RawMessage) (any, error)

// asyncOp validates its arguments and capabilities on the loop and returns the work to run
// off the loop.
type asyncOp func(i *Instance, inv *invocation, args json.RawMessage) (func(ctx context.Context) (any, error), error)

var (
	syncOps  map[string]syncOp
	asyncOps map[string]asyncOp
)

func init() {
	syncOps = map[string]syncOp{
		"crypto.random":          randomValues,
		"resolve.role":           opResolveRole,
		"server.channel":         opServerChannel,
		"server.role":            opServerRole,
		"server.audience":        opAudience,
		"server.compareAudience": opCompareAudience,
		"member.permissionsIn":   opPermissionsIn,
		"i18n.t":                 opTranslate,
		"server.owner":           opServerOwner,
		"status.set":             opFeatureStatus,
		"secrets.has":            opSecretHas,
		"it.state":               opInteractionState,
	}
	asyncOps = map[string]asyncOp{
		"channel.send":           opChannelSend,
		"channel.bulkDelete":     opChannelBulkDelete,
		"channel.purge":          opChannelPurge(false),
		"channel.previewPurge":   opChannelPurge(true),
		"channel.messages":       opChannelMessages,
		"channel.message":        opChannelMessage,
		"channel.createThread":   opChannelCreateThread,
		"message.reply":          opMessageReply,
		"message.edit":           opMessageEdit,
		"message.delete":         opMessageDelete,
		"message.react":          opMessageReact,
		"member.setRoles":        opMemberSetRoles,
		"member.addRole":         opMemberRole(true),
		"member.removeRole":      opMemberRole(false),
		"member.setNickname":     opMemberSetNickname,
		"member.timeout":         opMemberTimeout,
		"member.kick":            opMemberKick,
		"server.unban":           opServerUnban,
		"server.invites":         opServerInvites,
		"server.auditLog":        opAuditLog,
		"server.bans":            opServerBans,
		"member.ban":             opMemberBan,
		"member.send":            opMemberSend,
		"member.moveTo":          opMemberMoveTo,
		"server.member":          opServerMember,
		"server.voiceMembers":    opServerVoiceMembers,
		"role.members":           memberList(true),
		"server.members":         memberList(false),
		"server.createChannel":   opServerCreateChannel,
		"server.deleteChannel":   opServerDeleteChannel,
		"server.editChannel":     opServerEditChannel,
		"kv.entries":             advancedOp("entries"),
		"kv.atomic":              advancedOp("atomic"),
		"kv.rank":                advancedOp("rank"),
		"kv.get":                 opKVGet,
		"kv.set":                 opKVSet,
		"kv.delete":              opKVDelete,
		"kv.increment":           opKVIncrement,
		"kv.list":                opKVList,
		"schedule.after":         opScheduleAfter,
		"schedule.cancel":        opScheduleCancel,
		"files.read":             opFile("read"),
		"files.list":             opFile("list"),
		"files.bytes":            opFile("bytes"),
		"index.upsert":           opIndex("upsert"),
		"index.search":           opIndex("search"),
		"index.remove":           opIndex("remove"),
		"index.clear":            opIndex("clear"),
		"http.fetch":             opHTTPFetch,
		"events.publish":         opEventsPublish,
		"portal.link":            opPortalLink,
		"portal.revoke":          opPortalRevoke,
		"it.reply":               opInteractionReply,
		"it.defer":               opInteractionDefer,
		"it.followUp":            opInteractionFollowUp,
		"it.showModal":           opInteractionShowModal,
		"it.update":              opInteractionUpdate,
		"message.pin":            messaging("pin", capMessagesManage),
		"message.unpin":          messaging("unpin", capMessagesManage),
		"message.crosspost":      messaging("crosspost", capMessagesSend),
		"message.reactionUsers":  messaging("reactionUsers", capMessagesRead),
		"message.removeReaction": messaging("removeReaction", capMessagesManage),
		"channel.pins":           messaging("pins", capMessagesRead),
		"channel.typing":         messaging("typing", capMessagesSend),
		"channel.sendAs":         messaging("sendAs", "discord.webhooks.manage"),
		"server.overlayApply":    opOverlayApply,
		"server.overlayRemove":   opOverlayRemove,
		"server.overlays":        opOverlayList,
		"server.incident":        opIncident,
		"server.requestSync":     opRequestSync,
	}
}

// maxInFlight bounds the async host calls of one instance that are not settled yet.
const maxInFlight = 256

func checkArgs(op, args string) error {
	if len(args) > maxHostArgs {
		return argErr("%s: arguments larger than %d bytes", op, maxHostArgs)
	}
	return nil
}

func (i *Instance) call(op, args string) (string, bool, error) {
	f, ok := syncOps[op]
	if !ok {
		return "", false, fmt.Errorf("unknown host call %q", op)
	}
	if err := checkArgs(op, args); err != nil {
		return "", false, err
	}
	res, err := f(i, i.currentInv(), json.RawMessage(args))
	if err != nil || res == nil {
		return "", false, err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// callAsync starts an async host call and returns its id; the prelude's promise is settled
// in a later turn charged to the calling invocation.
func (i *Instance) callAsync(op, args string) (int64, error) {
	f, ok := asyncOps[op]
	if !ok {
		return 0, fmt.Errorf("unknown host call %q", op)
	}
	if err := checkArgs(op, args); err != nil {
		return 0, err
	}
	inv := i.currentInv()
	if err := inv.alive(); err != nil {
		return 0, err
	}
	if i.inflight >= maxInFlight {
		return 0, fmt.Errorf("%s: too many pending host calls (max %d per instance)", op, maxInFlight)
	}
	work, err := f(i, inv, json.RawMessage(args))
	if err != nil {
		return 0, err
	}
	i.nextCall++
	id := i.nextCall
	i.inflight++
	go func() {
		res, err := work(inv.ctx)
		if err == nil {
			inv.rememberAudience(res)
		}
		var data []byte
		if err == nil && res != nil {
			data, err = json.Marshal(res)
		}
		if err != nil && inv.ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			err = inv.alive()
		}
		status, payload := "ok", string(data)
		switch {
		case err != nil:
			status, payload = "err", errorJSON(err)
		case data == nil:
			status = "undef"
		}
		sid := strconv.FormatInt(id, 10)
		i.post(func() {
			i.inflight--
			rerr := i.enter(inv, i.limits.SyncBudget, func(ctx context.Context) error {
				_, err := i.vm.runAPI(ctx, "settle", sid, status, payload)
				return err
			})
			if rerr != nil && !isInterrupt(rerr) && !errors.Is(rerr, ErrCrashed) {
				i.logger.Error("plugin host: settle host call", "op", op, "error", rerr)
			}
		})
	}()
	return id, nil
}

func (i *Instance) log(level, msg, fields string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	attrs := make([]slog.Attr, 0, 8)
	if inv := i.currentInv(); inv != i.bg {
		attrs = append(attrs, slog.String("handler", inv.kind), slog.String("name", inv.name))
	}
	if len(fields) > maxLogFields {
		attrs = append(attrs, slog.String("fields", fmt.Sprintf("[%d bytes, larger than %d: dropped]", len(fields), maxLogFields)))
		fields = ""
	}
	if fields != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(fields), &m); err == nil {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				attrs = append(attrs, slog.Any(k, m[k]))
			}
		} else {
			attrs = append(attrs, slog.String("fields", fields))
		}
	}
	if len(msg) > 4096 {
		msg = msg[:4096] + "…"
	}
	i.logger.LogAttrs(context.Background(), lvl, msg, attrs...)
}

func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(args, &v); err != nil {
		return v, argErr("invalid arguments: %v", err)
	}
	return v, nil
}

// ───────────────────────────────────────────────────────────── references

// ref is a ResourceRef: a key, or {id}.
type ref struct {
	Key string
	ID  string
	set bool
}

func (r *ref) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	r.set = true
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &r.Key)
	}
	var o struct {
		ID any `json:"id"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return errors.New("a reference must be a key or { id }")
	}
	switch id := o.ID.(type) {
	case string:
		r.ID = id
	case float64:
		return errors.New("ids must be strings (snowflakes do not fit in a JS number)")
	default:
		return errors.New("a reference must be a key or { id }")
	}
	return nil
}

func (r ref) String() string {
	if r.ID != "" {
		return fmt.Sprintf("{ id: %q }", r.ID)
	}
	return fmt.Sprintf("%q", r.Key)
}

var snowflakeRe = regexp.MustCompile(`^[0-9]{1,20}$`)

func checkSnowflake(what, id string) error {
	if !snowflakeRe.MatchString(id) {
		return argErr("invalid %s id %q", what, id)
	}
	return nil
}

// resolve returns the Discord ID of a reference.
func (i *Instance) resolve(kind state.ResourceKind, r ref) (string, error) {
	if !r.set || (r.Key == "" && r.ID == "") {
		return "", argErr("a %s reference is required", kind)
	}
	if r.ID != "" {
		if err := checkSnowflake(string(kind), r.ID); err != nil {
			return "", err
		}
		return r.ID, nil
	}
	if i.env.Resolver != nil {
		if id, ok := i.env.Resolver.ID(kind, r.Key); ok {
			return id, nil
		}
	}
	return "", fmt.Errorf("unknown %s %q: it is not declared in servers/%s.yaml (use { id } for unmanaged resources)", kind, r.Key, i.env.ServerKey)
}

func opResolveRole(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	a, err := decode[struct{ Ref ref }](args)
	if err != nil {
		return nil, err
	}
	return i.resolve(state.KindRole, a.Ref)
}

func opServerChannel(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	if err := i.require(capChannelsRead); err != nil {
		return nil, err
	}
	a, err := decode[struct{ Ref ref }](args)
	if err != nil {
		return nil, err
	}
	id, err := i.resolve(state.KindChannel, a.Ref)
	if err != nil {
		return nil, err
	}
	if i.env.Resolver != nil {
		if ch, ok := i.env.Resolver.Channel(id); ok {
			if ch.ServerID != "" && ch.ServerID != i.env.ServerID {
				return nil, fmt.Errorf("channel %s does not belong to this server", id)
			}
			i.markServerChannel(id)
			return i.channelDTOFrom(ch), nil
		}
	}
	if a.Ref.Key != "" || i.isServerChannel(id) {
		i.markServerChannel(id)
		return i.channelDTO(id), nil
	}
	return nil, fmt.Errorf("unknown channel %s on server %q", a.Ref, i.env.ServerKey)
}

func opServerRole(i *Instance, _ *invocation, args json.RawMessage) (any, error) {
	a, err := decode[struct{ Ref ref }](args)
	if err != nil {
		return nil, err
	}
	id, err := i.resolve(state.KindRole, a.Ref)
	if err != nil {
		return nil, err
	}
	if i.env.Resolver != nil {
		if _, ok := i.env.Resolver.Role(id); ok {
			return i.roleDTO(id), nil
		}
	}
	if a.Ref.Key != "" || id == i.env.ServerID {
		return i.roleDTO(id), nil
	}
	return nil, fmt.Errorf("unknown role %s on server %q", a.Ref, i.env.ServerKey)
}

// opServerOwner returns the owner's user id, or null when it is not known.
func opServerOwner(i *Instance, _ *invocation, _ json.RawMessage) (any, error) {
	if id := i.ownerID(); id != "" {
		return id, nil
	}
	return nil, nil
}

// ───────────────────────────────────────────────────────────── channel scope

func (i *Instance) markServerChannel(id string) {
	i.chMu.Lock()
	i.serverChannels[id] = true
	i.chMu.Unlock()
}

func (i *Instance) markDMChannel(id string) {
	i.chMu.Lock()
	i.dmChannels[id] = true
	i.chMu.Unlock()
}

func (i *Instance) isServerChannel(id string) bool {
	i.chMu.Lock()
	defer i.chMu.Unlock()
	return i.serverChannels[id]
}

func (i *Instance) isDMChannel(id string) bool {
	i.chMu.Lock()
	defer i.chMu.Unlock()
	return i.dmChannels[id]
}

// channelScope checks that a channel belongs to this instance's server (or is a DM channel
// this instance opened or received) and reports whether it is a DM. It keeps a plugin from
// reaching channels of other servers the bot is in.
func (i *Instance) channelScope(ctx context.Context, id string) (dm bool, err error) {
	if err := checkSnowflake("channel", id); err != nil {
		return false, err
	}
	if i.isDMChannel(id) {
		return true, nil
	}
	if i.isServerChannel(id) {
		return false, nil
	}
	if i.env.Resolver != nil {
		if ch, ok := i.env.Resolver.Channel(id); ok && (ch.ServerID == "" || ch.ServerID == i.env.ServerID) {
			i.markServerChannel(id)
			return false, nil
		}
	}
	ch, err := i.env.Discord.GetChannel(ctx, id)
	if err != nil {
		return false, fmt.Errorf("channel %s: %w", id, err)
	}
	if ch.ServerID != i.env.ServerID {
		return false, fmt.Errorf("channel %s does not belong to this server", id)
	}
	i.markServerChannel(id)
	return false, nil
}

func (i *Instance) sendCapability(dm bool) string {
	if dm {
		return capDMSend
	}
	return capMessagesSend
}

// ───────────────────────────────────────────────────────────── channels & messages

type channelMessageArgs struct {
	ChannelID string       `json:"channelId"`
	MessageID string       `json:"messageId"`
	AuthorID  string       `json:"authorId"`
	Message   messageInput `json:"message"`
	Reason    string       `json:"reason"`
	Emoji     string       `json:"emoji"`
}

func opChannelSend(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := decode[channelMessageArgs](args)
	if err != nil {
		return nil, err
	}
	mc, err := i.messageCreate(a.Message, false)
	if err != nil {
		return nil, err
	}
	if err := i.precheckSend(a.ChannelID); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		dm, err := i.channelScope(ctx, a.ChannelID)
		if err != nil {
			return nil, err
		}
		if err := i.require(i.sendCapability(dm)); err != nil {
			return nil, err
		}
		m, err := i.env.Discord.SendMessage(ctx, a.ChannelID, i.scopeNonce(a.ChannelID, mc))
		if err != nil {
			return nil, err
		}
		return i.messageDTO(m), nil
	}, nil
}

// precheckSend fails early (synchronously) when the send capability is certainly missing.
func (i *Instance) precheckSend(channelID string) error {
	if !i.grants[capMessagesSend] && !i.grants[capDMSend] {
		return &CapabilityError{Capability: capMessagesSend}
	}
	if i.isDMChannel(channelID) {
		return i.require(capDMSend)
	}
	if i.isServerChannel(channelID) {
		return i.require(capMessagesSend)
	}
	return nil
}

func opChannelMessages(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capMessagesRead); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		ChannelID string `json:"channelId"`
		Limit     int    `json:"limit"`
		Before    string `json:"before"`
		After     string `json:"after"`
	}](args)
	if err != nil {
		return nil, err
	}
	if a.Limit < 0 || a.Limit > 100 {
		return nil, argErr("messages(): limit must be between 1 and 100")
	}
	return func(ctx context.Context) (any, error) {
		if _, err := i.channelScope(ctx, a.ChannelID); err != nil {
			return nil, err
		}
		list, err := i.env.Discord.ListMessages(ctx, a.ChannelID, discord.ListMessagesParams{Limit: a.Limit, Before: a.Before, After: a.After})
		if err != nil {
			return nil, err
		}
		out := make([]messageDTO, len(list))
		for k, m := range list {
			out[k] = i.messageDTO(m)
		}
		return out, nil
	}, nil
}

// opChannelMessage fetches one message; a message that does not exist (or was deleted) is
// undefined in JS, not an error.
func opChannelMessage(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capMessagesRead); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		ChannelID string `json:"channelId"`
		MessageID string `json:"messageId"`
	}](args)
	if err != nil {
		return nil, err
	}
	if err := checkSnowflake("message", a.MessageID); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		if _, err := i.channelScope(ctx, a.ChannelID); err != nil {
			return nil, err
		}
		m, err := i.env.Discord.GetMessage(ctx, a.ChannelID, a.MessageID)
		if errors.Is(err, discord.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return i.messageDTO(m), nil
	}, nil
}

func opChannelCreateThread(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capThreadsManage); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		ChannelID   string `json:"channelId"`
		Name        string `json:"name"`
		Private     bool   `json:"private"`
		FromMessage string `json:"fromMessage"`
	}](args)
	if err != nil {
		return nil, err
	}
	if a.Name == "" || len([]rune(a.Name)) > 100 {
		return nil, argErr("createThread(): name must be 1-100 characters")
	}
	return func(ctx context.Context) (any, error) {
		if _, err := i.channelScope(ctx, a.ChannelID); err != nil {
			return nil, err
		}
		ch, err := i.env.Discord.CreateThread(ctx, a.ChannelID, discord.ThreadParams{Name: a.Name, Private: a.Private, MessageID: a.FromMessage})
		if err != nil {
			return nil, err
		}
		i.markServerChannel(ch.ID)
		return i.channelDTOFrom(ch), nil
	}, nil
}

func opMessageReply(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := decode[channelMessageArgs](args)
	if err != nil {
		return nil, err
	}
	mc, err := i.messageCreate(a.Message, false)
	if err != nil {
		return nil, err
	}
	if err := checkSnowflake("message", a.MessageID); err != nil {
		return nil, err
	}
	mc.ReplyTo = a.MessageID
	if err := i.precheckSend(a.ChannelID); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		dm, err := i.channelScope(ctx, a.ChannelID)
		if err != nil {
			return nil, err
		}
		if err := i.require(i.sendCapability(dm)); err != nil {
			return nil, err
		}
		m, err := i.env.Discord.SendMessage(ctx, a.ChannelID, i.scopeNonce(a.ChannelID, mc))
		if err != nil {
			return nil, err
		}
		return i.messageDTO(m), nil
	}, nil
}

func opMessageEdit(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := decode[channelMessageArgs](args)
	if err != nil {
		return nil, err
	}
	mc, err := i.messageCreate(a.Message, false)
	if err != nil {
		return nil, err
	}
	if err := checkSnowflake("message", a.MessageID); err != nil {
		return nil, err
	}
	// Editing the bot's own messages needs the send capability; other messages need
	// messages.manage (Discord itself only lets authors edit their messages, so authorId,
	// which comes from plugin code, cannot widen access).
	own := a.AuthorID != "" && a.AuthorID == i.env.Bot.ID
	if !own {
		if err := i.require(capMessagesManage); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) (any, error) {
		dm, err := i.channelScope(ctx, a.ChannelID)
		if err != nil {
			return nil, err
		}
		if own {
			if err := i.require(i.sendCapability(dm)); err != nil {
				return nil, err
			}
		}
		m, err := i.env.Discord.EditMessage(ctx, a.ChannelID, a.MessageID, mc)
		if err != nil {
			return nil, err
		}
		return i.messageDTO(m), nil
	}, nil
}

func opMessageDelete(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capMessagesManage); err != nil {
		return nil, err
	}
	a, err := decode[channelMessageArgs](args)
	if err != nil {
		return nil, err
	}
	if err := checkSnowflake("message", a.MessageID); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		if _, err := i.channelScope(ctx, a.ChannelID); err != nil {
			return nil, err
		}
		return nil, i.env.Discord.DeleteMessage(ctx, a.ChannelID, a.MessageID, a.Reason)
	}, nil
}

func opMessageReact(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capReactions); err != nil {
		return nil, err
	}
	a, err := decode[channelMessageArgs](args)
	if err != nil {
		return nil, err
	}
	if a.Emoji == "" {
		return nil, argErr("react(): emoji is required")
	}
	if err := checkSnowflake("message", a.MessageID); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		if _, err := i.channelScope(ctx, a.ChannelID); err != nil {
			return nil, err
		}
		return nil, i.env.Discord.AddReaction(ctx, a.ChannelID, a.MessageID, a.Emoji)
	}, nil
}

// ───────────────────────────────────────────────────────────── members

type memberArgs struct {
	UserID                string       `json:"userId"`
	Role                  ref          `json:"role"`
	Reason                string       `json:"reason"`
	Nickname              string       `json:"nickname"`
	DurationMs            float64      `json:"durationMs"`
	DeleteMessagesSeconds int          `json:"deleteMessagesSeconds"`
	Message               messageInput `json:"message"`
}

func memberOp(i *Instance, capability string, args json.RawMessage) (memberArgs, error) {
	if capability != "" {
		if err := i.require(capability); err != nil {
			return memberArgs{}, err
		}
	}
	a, err := decode[memberArgs](args)
	if err != nil {
		return a, err
	}
	return a, checkSnowflake("user", a.UserID)
}

func opMemberRole(add bool) asyncOp {
	return func(i *Instance, inv *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		a, err := memberOp(i, capRolesAssign, args)
		if err != nil {
			return nil, err
		}
		roleID, err := i.resolve(state.KindRole, a.Role)
		if err != nil {
			return nil, err
		}
		if roleID == i.env.ServerID {
			return nil, argErr("the @everyone role cannot be assigned")
		}
		return func(ctx context.Context) (any, error) {
			var err error
			if add {
				err = i.env.Discord.AddMemberRole(ctx, i.env.ServerID, a.UserID, roleID, a.Reason)
			} else {
				err = i.env.Discord.RemoveMemberRole(ctx, i.env.ServerID, a.UserID, roleID, a.Reason)
			}
			if err != nil {
				return nil, err
			}
			if inv != nil {
				if existing, ok := inv.audienceMembers.Load(a.UserID); ok {
					member := existing.(discord.Member)
					roles := slices.Clone(member.RoleIDs)
					if add && !slices.Contains(roles, roleID) {
						roles = append(roles, roleID)
					} else if !add {
						roles = slices.DeleteFunc(roles, func(id string) bool { return id == roleID })
					}
					member.RoleIDs = roles
					inv.audienceMembers.Store(a.UserID, member)
				}
			}
			return i.roleDTO(roleID), nil
		}, nil
	}
}

// opMemberSetRoles changes the full role set in one Discord PATCH. Before replacing
// it, compare with current membership to protect both additions and implicit removals.
func opMemberSetRoles(i *Instance, inv *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capRolesAssign); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		UserID string `json:"userId"`
		Roles  []ref  `json:"roles"`
		Reason string `json:"reason"`
	}](args)
	if err != nil {
		return nil, err
	}
	if err := checkSnowflake("user", a.UserID); err != nil {
		return nil, err
	}
	if a.Roles == nil || len(a.Roles) > 250 {
		return nil, argErr("setRoles(): expected at most 250 roles")
	}
	ids := make([]string, 0, len(a.Roles))
	wanted := map[string]bool{}
	for _, role := range a.Roles {
		id, err := i.resolve(state.KindRole, role)
		if err != nil {
			return nil, err
		}
		if id == i.env.ServerID || wanted[id] {
			return nil, argErr("setRoles(): duplicate role or @everyone")
		}
		ids = append(ids, id)
		wanted[id] = true
	}
	protected := map[string]bool{}
	for _, key := range i.env.DeclaredMemberRoles {
		id, err := i.resolve(state.KindRole, ref{Key: key, set: true})
		if err != nil {
			return nil, err
		}
		protected[id] = true
	}
	return func(ctx context.Context) (any, error) {
		member, err := i.env.Discord.GetMember(ctx, i.env.ServerID, a.UserID)
		if err != nil {
			return nil, err
		}
		for id := range protected {
			if wanted[id] != member.HasRole(id) {
				return nil, argErr("setRoles(): membership of role %s is declared in git", id)
			}
		}
		// Discord enforces the managing bot's role hierarchy and integration roles.
		result, err := i.env.Discord.UpdateMember(ctx, i.env.ServerID, a.UserID, discord.MemberParams{RoleIDs: ids}, a.Reason)
		if err != nil {
			return nil, err
		}
		dto := i.memberDTO(result)
		if inv != nil {
			inv.rememberAudience(dto)
		}
		return dto.Roles, nil
	}, nil
}

func opMemberSetNickname(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := memberOp(i, capMembersManage, args)
	if err != nil {
		return nil, err
	}
	if len([]rune(a.Nickname)) > 32 {
		return nil, argErr("setNickname(): at most 32 characters")
	}
	return func(ctx context.Context) (any, error) {
		nick := a.Nickname
		_, err := i.env.Discord.UpdateMember(ctx, i.env.ServerID, a.UserID, discord.MemberParams{Nick: &nick}, a.Reason)
		return nil, err
	}, nil
}

func opMemberTimeout(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := memberOp(i, capMembersModerate, args)
	if err != nil {
		return nil, err
	}
	if a.DurationMs != a.DurationMs || a.DurationMs > float64(28*24*time.Hour/time.Millisecond) {
		return nil, argErr("timeout(): duration must be at most 28 days")
	}
	return func(ctx context.Context) (any, error) {
		var until time.Time // zero: remove the timeout
		if a.DurationMs > 0 {
			until = time.Now().Add(time.Duration(a.DurationMs) * time.Millisecond)
		}
		_, err := i.env.Discord.UpdateMember(ctx, i.env.ServerID, a.UserID, discord.MemberParams{TimeoutUntil: &until}, a.Reason)
		return nil, err
	}, nil
}

func opMemberKick(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := memberOp(i, capMembersModerate, args)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		return nil, i.env.Discord.KickMember(ctx, i.env.ServerID, a.UserID, a.Reason)
	}, nil
}

func opMemberBan(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := memberOp(i, capMembersModerate, args)
	if err != nil {
		return nil, err
	}
	if a.DeleteMessagesSeconds < 0 || a.DeleteMessagesSeconds > 7*24*3600 {
		return nil, argErr("ban(): deleteMessagesSeconds must be between 0 and 604800")
	}
	return func(ctx context.Context) (any, error) {
		return nil, i.env.Discord.BanMember(ctx, i.env.ServerID, a.UserID, a.DeleteMessagesSeconds, a.Reason)
	}, nil
}

func opServerUnban(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := memberOp(i, capMembersModerate, args)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		err := i.env.Discord.UnbanMember(ctx, i.env.ServerID, a.UserID, a.Reason)
		// An already-lifted ban is success, including when an expiry runs again after restart.
		if errors.Is(err, discord.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}, nil
}
func opServerBans(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capMembersModerate); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		Limit *int   `json:"limit"`
		After string `json:"after"`
	}](args)
	if err != nil {
		return nil, err
	}
	limit := 100
	if a.Limit != nil {
		limit = *a.Limit
	}
	if limit < 1 || limit > 1000 {
		return nil, argErr("bans(): limit must be between 1 and 1000")
	}
	if a.After != "" {
		if err := checkSnowflake("after", a.After); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) (any, error) {
		bans, err := i.env.Discord.ListBans(ctx, i.env.ServerID, a.After, limit)
		if err != nil {
			return nil, err
		}
		type entry struct {
			User   userDTO `json:"user"`
			Reason string  `json:"reason,omitempty"`
		}
		out := make([]entry, 0, len(bans))
		for _, b := range bans {
			out = append(out, entry{User: i.userDTO(b.User), Reason: b.Reason})
		}
		return out, nil
	}, nil
}

func opMemberSend(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := memberOp(i, capDMSend, args)
	if err != nil {
		return nil, err
	}
	mc, err := i.messageCreate(a.Message, false)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		ch, err := i.env.Discord.CreateDM(ctx, a.UserID)
		if err != nil {
			return nil, err
		}
		i.markDMChannel(ch.ID)
		m, err := i.env.Discord.SendMessage(ctx, ch.ID, i.scopeNonce(ch.ID, mc))
		if err != nil {
			return nil, err
		}
		return i.messageDTO(m), nil
	}, nil
}

// opMemberMoveTo moves a member connected to voice to a voice channel, or disconnects
// them (disconnect: true).
func opMemberMoveTo(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capVoiceMove); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		UserID     string `json:"userId"`
		Channel    ref    `json:"channel"`
		Disconnect bool   `json:"disconnect"`
		Reason     string `json:"reason"`
	}](args)
	if err != nil {
		return nil, err
	}
	if err := checkSnowflake("user", a.UserID); err != nil {
		return nil, err
	}
	target := ""
	if !a.Disconnect {
		if target, err = i.resolve(state.KindChannel, a.Channel); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) (any, error) {
		if target != "" {
			if _, err := i.channelScope(ctx, target); err != nil {
				return nil, err
			}
		}
		_, err := i.env.Discord.UpdateMember(ctx, i.env.ServerID, a.UserID, discord.MemberParams{VoiceChannelID: &target}, a.Reason)
		return nil, err
	}, nil
}

// ───────────────────────────────────────────────────────────── server

func opServerMember(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := memberOp(i, capMembersRead, args)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		m, err := i.env.Discord.GetMember(ctx, i.env.ServerID, a.UserID)
		if errors.Is(err, discord.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		dto := i.memberDTO(m)
		return &dto, nil
	}, nil
}

// role.members() limits.
const (
	defaultRoleMembers = 100
	maxRoleMembers     = 1000
)

// opRoleMembers lists the members holding a role, in ascending user ID order. Discord has
// no such endpoint: the server's members are listed (Env.Members) and filtered.
func memberList(role bool) asyncOp {
	scope := "server.members()"
	if role {
		scope = "role.members()"
	}
	return func(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
		if err := i.require(capMembersRead); err != nil {
			return nil, err
		}
		if !i.env.ServerMembers {
			return nil, errors.New(scope + " needs the ServerMembers intent: declare it in plugin.yaml (spec.intents) " +
				"and enable Server Members Intent for the bot in the Discord developer portal")
		}
		a, err := decode[struct {
			Role  ref      `json:"role"`
			Limit *float64 `json:"limit"`
			After string   `json:"after"`
		}](args)
		if err != nil {
			return nil, err
		}
		var roleID string
		if role {
			roleID, err = i.resolve(state.KindRole, a.Role)
			if err != nil {
				return nil, err
			}
			if roleID == i.env.ServerID {
				return nil, argErr("role.members(): every member has the @everyone role; pass a role")
			}
		}
		limit := defaultRoleMembers
		if a.Limit != nil {
			l := *a.Limit
			if l != float64(int(l)) || l < 1 || l > maxRoleMembers {
				return nil, argErr(scope+": limit must be an integer between 1 and %d", maxRoleMembers)
			}
			limit = int(l)
		}
		if a.After != "" {
			if err := checkSnowflake("user", a.After); err != nil {
				return nil, err
			}
		}
		list := i.env.Members
		if list == nil {
			ml, ok := i.env.Discord.(discord.MemberLister)
			if !ok {
				return nil, errors.New(scope + ": this Discord client cannot list members")
			}
			list = func(ctx context.Context) ([]discord.Member, error) { return ml.ListMembers(ctx, i.env.ServerID) }
		}
		return func(ctx context.Context) (any, error) {
			all, err := list(ctx)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", scope, err)
			}
			var held []discord.Member
			for _, m := range all {
				if (!role || m.HasRole(roleID)) && (a.After == "" || discord.SnowflakeLess(a.After, m.User.ID)) {
					held = append(held, m)
				}
			}
			sort.Slice(held, func(x, y int) bool { return discord.SnowflakeLess(held[x].User.ID, held[y].User.ID) })
			if len(held) > limit {
				held = held[:limit]
			}
			out := make([]memberDTO, 0, len(held))
			for _, m := range held {
				out = append(out, i.memberDTO(m))
			}
			return out, nil
		}, nil
	}

}

// errVoiceStatesUnknown: the runtime has not received the server's voice states yet (the
// gateway sends them when the server becomes available). An empty answer would be a lie.
var errVoiceStatesUnknown = errors.New("server.voiceMembers(): the voice states of the server are not known yet " +
	"(the gateway has not sent them): try again later")

// opServerVoiceMembers lists the members connected to a voice channel, from the runtime's
// voice state cache (no Discord endpoint lists them).
func opServerVoiceMembers(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capMembersRead); err != nil {
		return nil, err
	}
	if !i.env.ServerVoiceStates {
		return nil, errors.New("server.voiceMembers() needs the ServerVoiceStates intent: declare it in plugin.yaml (spec.intents)")
	}
	a, err := decode[struct {
		Ref ref `json:"ref"`
	}](args)
	if err != nil {
		return nil, err
	}
	id, err := i.resolve(state.KindChannel, a.Ref)
	if err != nil {
		return nil, err
	}
	if i.env.VoiceStates == nil {
		return nil, errVoiceStatesUnknown
	}
	states, known := i.env.VoiceStates()
	if !known {
		return nil, errVoiceStatesUnknown
	}
	var in []discord.VoiceState
	for _, st := range states {
		if st.ChannelID == id {
			in = append(in, st)
		}
	}
	sort.Slice(in, func(x, y int) bool { return discord.SnowflakeLess(in[x].UserID, in[y].UserID) })
	return func(ctx context.Context) (any, error) {
		out := make([]memberDTO, 0, len(in))
		for _, st := range in {
			m := st.Member
			if m == nil || m.User.ID == "" {
				got, err := i.env.Discord.GetMember(ctx, i.env.ServerID, st.UserID)
				if errors.Is(err, discord.ErrNotFound) {
					continue // left the server
				}
				if err != nil {
					return nil, err
				}
				m = &got
			}
			out = append(out, i.memberDTO(*m))
		}
		return out, nil
	}, nil
}

// owner is the ResourceMap owner of plugin-created resources.
func (i *Instance) owner() string { return "plugin:" + i.env.BotKey + "/" + i.env.InstanceName }

func opServerCreateChannel(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capChannelsManage); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		Name       string   `json:"name"`
		Type       string   `json:"type"`
		Category   ref      `json:"category"`
		Topic      string   `json:"topic"`
		UserLimit  *float64 `json:"userLimit"`
		Bitrate    *float64 `json:"bitrate"`
		Overwrites map[string]struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"overwrites"`
	}](args)
	if err != nil {
		return nil, err
	}
	if a.Name == "" || len([]rune(a.Name)) > 100 {
		return nil, argErr("createChannel(): name must be 1-100 characters")
	}
	p := discord.ChannelParams{Name: &a.Name}
	switch a.Type {
	case "", "text":
		p.Type = discord.ChannelText
	case "voice":
		p.Type = discord.ChannelVoice
	case "forum":
		p.Type = discord.ChannelForum
	default:
		return nil, argErr("createChannel(): type must be text, voice or forum")
	}
	if a.Category.set {
		id, err := i.resolve(state.KindCategory, a.Category)
		if err != nil {
			return nil, err
		}
		p.ParentID = &id
	}
	if a.Topic != "" {
		p.Topic = &a.Topic
	}
	if p.UserLimit, p.Bitrate, err = voiceSettings("createChannel", p.Type == discord.ChannelVoice, a.UserLimit, a.Bitrate); err != nil {
		return nil, err
	}
	if len(a.Overwrites) > 0 {
		targets := make([]string, 0, len(a.Overwrites))
		for t := range a.Overwrites {
			targets = append(targets, t)
		}
		sort.Strings(targets)
		ows := make([]discord.Overwrite, 0, len(targets))
		for _, target := range targets {
			spec := a.Overwrites[target]
			ow, err := i.overwriteTarget("createChannel", target)
			if err != nil {
				return nil, err
			}
			if ow.Allow, err = discord.ParsePermissions(spec.Allow); err != nil {
				return nil, argErr("createChannel(): overwrite %q: %v", target, err)
			}
			if ow.Deny, err = discord.ParsePermissions(spec.Deny); err != nil {
				return nil, argErr("createChannel(): overwrite %q: %v", target, err)
			}
			ows = append(ows, ow)
		}
		p.Overwrites = &ows
	}
	return func(ctx context.Context) (any, error) {
		ch, err := i.env.Discord.CreateChannel(ctx, i.env.ServerID, p, "cicord: created by plugin "+i.env.PluginName+" ("+i.env.InstanceName+")")
		if err != nil {
			return nil, err
		}
		i.markServerChannel(ch.ID)
		if i.env.Resources != nil {
			r := state.Resource{
				Server: i.env.ServerKey, Kind: state.KindChannel,
				Key: i.env.BotKey + "/" + i.env.InstanceName + "/" + ch.ID, ID: ch.ID,
				Owner: i.owner(), UpdatedAt: time.Now().UTC(),
			}
			if err := i.env.Resources.Put(ctx, r); err != nil {
				i.logger.Error("plugin channel created but its ownership could not be recorded", "channel", ch.ID, "error", err)
			}
		} else {
			i.logger.Warn("plugin channel created without a resource map: ownership not recorded", "channel", ch.ID)
		}
		return i.channelDTOFrom(ch), nil
	}, nil
}

// overwriteTarget parses an overwrite target of createChannel / editChannel: "@everyone",
// a role key, "role:<id>" or "member:<id>".
func (i *Instance) overwriteTarget(fn, target string) (discord.Overwrite, error) {
	ow := discord.Overwrite{Type: discord.OverwriteRole}
	switch {
	case target == "@everyone":
		ow.ID = i.env.ServerID
	case strings.HasPrefix(target, "member:"):
		ow.ID, ow.Type = strings.TrimPrefix(target, "member:"), discord.OverwriteMember
		if err := checkSnowflake("member", ow.ID); err != nil {
			return ow, err
		}
	case strings.HasPrefix(target, "role:"):
		ow.ID = strings.TrimPrefix(target, "role:")
		if err := checkSnowflake("role", ow.ID); err != nil {
			return ow, err
		}
	default:
		id, err := i.resolve(state.KindRole, ref{Key: target, set: true})
		if err != nil {
			return ow, fmt.Errorf("%s(): overwrite target %q: %w (use @everyone, a role key, role:<id> or member:<id>)", fn, target, err)
		}
		ow.ID = id
	}
	return ow, nil
}

// Voice channel settings (Discord's limits; the highest bitrates need server boosts).
const (
	maxUserLimit = 99
	minBitrate   = 8000
	maxBitrate   = 384000
)

// voiceSettings validates userLimit (0 = no limit) and bitrate, which only voice channels
// have.
func voiceSettings(fn string, voice bool, userLimit, bitrate *float64) (*int, *int, error) {
	var ul, br *int
	if userLimit != nil {
		v := *userLimit
		if !voice {
			return nil, nil, argErr("%s(): userLimit is only for voice channels", fn)
		}
		if v != float64(int(v)) || v < 0 || v > maxUserLimit {
			return nil, nil, argErr("%s(): userLimit must be an integer between 0 (no limit) and %d", fn, maxUserLimit)
		}
		n := int(v)
		ul = &n
	}
	if bitrate != nil {
		v := *bitrate
		if !voice {
			return nil, nil, argErr("%s(): bitrate is only for voice channels", fn)
		}
		if v != float64(int(v)) || v < minBitrate || v > maxBitrate {
			return nil, nil, argErr("%s(): bitrate must be an integer between %d and %d (bits per second)", fn, minBitrate, maxBitrate)
		}
		n := int(v)
		br = &n
	}
	return ul, br, nil
}

// overwritePatch changes one overwrite target of editChannel: allow and deny set those
// bits (taking them out of the other side), clear makes them neutral. Other bits and
// other targets are kept.
type overwritePatch struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
	Clear []string `json:"clear"`
}

// ownedChannel checks that the plugin instance owns a channel (it created it).
func (i *Instance) ownedChannel(ctx context.Context, fn, id string) (state.Resource, error) {
	if i.env.Resources == nil {
		return state.Resource{}, fmt.Errorf("%s(): channel ownership is not available (no resource map)", fn)
	}
	r, err := i.env.Resources.GetByID(ctx, i.env.ServerKey, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && r.Owner != i.owner()) {
		return r, fmt.Errorf("%s(): channel %s is not owned by this plugin instance", fn, id)
	}
	return r, err
}

// opServerEditChannel edits a channel owned by the plugin instance. Overwrites are
// patched (overwritePatch, null removes a target), so that the overwrites a channel got
// from its category, or from other edits, are kept.
func opServerEditChannel(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capChannelsManage); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		Ref        ref                        `json:"ref"`
		Name       *string                    `json:"name"`
		Topic      *string                    `json:"topic"`
		UserLimit  *float64                   `json:"userLimit"`
		Bitrate    *float64                   `json:"bitrate"`
		Overwrites map[string]*overwritePatch `json:"overwrites"`
		Reason     string                     `json:"reason"`
	}](args)
	if err != nil {
		return nil, err
	}
	id, err := i.resolve(state.KindChannel, a.Ref)
	if err != nil {
		return nil, err
	}
	if a.Name != nil && (*a.Name == "" || len([]rune(*a.Name)) > 100) {
		return nil, argErr("editChannel(): name must be 1-100 characters")
	}
	if a.Topic != nil && len([]rune(*a.Topic)) > 1024 {
		return nil, argErr("editChannel(): topic must be at most 1024 characters")
	}
	// Overwrite targets and bits are parsed here (sorted: deterministic errors).
	type patch struct {
		ow                 discord.Overwrite
		remove             bool
		allow, deny, clear discord.Permissions
	}
	targets := make([]string, 0, len(a.Overwrites))
	for t := range a.Overwrites {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	patches := make([]patch, 0, len(targets))
	for _, target := range targets {
		ow, err := i.overwriteTarget("editChannel", target)
		if err != nil {
			return nil, err
		}
		pt := patch{ow: ow, remove: a.Overwrites[target] == nil}
		if spec := a.Overwrites[target]; spec != nil {
			for _, f := range []struct {
				dst   *discord.Permissions
				names []string
			}{{&pt.allow, spec.Allow}, {&pt.deny, spec.Deny}, {&pt.clear, spec.Clear}} {
				if *f.dst, err = discord.ParsePermissions(f.names); err != nil {
					return nil, argErr("editChannel(): overwrite %q: %v", target, err)
				}
			}
			if pt.allow&pt.deny != 0 || (pt.allow|pt.deny)&pt.clear != 0 {
				return nil, argErr("editChannel(): overwrite %q: a permission is in more than one of allow, deny and clear", target)
			}
		}
		patches = append(patches, pt)
	}
	reason := a.Reason
	if reason == "" {
		reason = "cicord: edited by plugin " + i.env.PluginName + " (" + i.env.InstanceName + ")"
	}
	return func(ctx context.Context) (any, error) {
		if _, err := i.ownedChannel(ctx, "editChannel", id); err != nil {
			return nil, err
		}
		cur, err := i.env.Discord.GetChannel(ctx, id)
		if err != nil {
			return nil, err
		}
		p := discord.ChannelParams{Name: a.Name, Topic: a.Topic}
		if p.UserLimit, p.Bitrate, err = voiceSettings("editChannel", cur.Type == discord.ChannelVoice || cur.Type == discord.ChannelStage, a.UserLimit, a.Bitrate); err != nil {
			return nil, err
		}
		if len(patches) > 0 {
			ows := append([]discord.Overwrite(nil), cur.Overwrites...)
			for _, pt := range patches {
				idx := slices.IndexFunc(ows, func(o discord.Overwrite) bool { return o.ID == pt.ow.ID && o.Type == pt.ow.Type })
				if pt.remove {
					if idx >= 0 {
						ows = slices.Delete(ows, idx, idx+1)
					}
					continue
				}
				if idx < 0 {
					ows = append(ows, pt.ow)
					idx = len(ows) - 1
				}
				o := &ows[idx]
				o.Allow = (o.Allow &^ (pt.deny | pt.clear)) | pt.allow
				o.Deny = (o.Deny &^ (pt.allow | pt.clear)) | pt.deny
				if o.Allow == 0 && o.Deny == 0 && o.Type == discord.OverwriteMember {
					ows = slices.Delete(ows, idx, idx+1) // an empty member overwrite is noise
				}
			}
			p.Overwrites = &ows
		}
		ch, err := i.env.Discord.UpdateChannel(ctx, id, p, reason)
		if err != nil {
			return nil, err
		}
		return i.channelDTOFrom(ch), nil
	}, nil
}

func opServerDeleteChannel(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capChannelsManage); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		Ref    ref    `json:"ref"`
		Reason string `json:"reason"`
	}](args)
	if err != nil {
		return nil, err
	}
	id, err := i.resolve(state.KindChannel, a.Ref)
	if err != nil {
		return nil, err
	}
	if i.env.Resources == nil {
		return nil, errors.New("deleteChannel(): channel ownership is not available (no resource map)")
	}
	return func(ctx context.Context) (any, error) {
		r, err := i.ownedChannel(ctx, "deleteChannel", id)
		if err != nil {
			return nil, err
		}
		if err := i.env.Discord.DeleteChannel(ctx, id, a.Reason); err != nil && !errors.Is(err, discord.ErrNotFound) {
			return nil, err
		}
		return nil, i.env.Resources.Delete(ctx, i.env.ServerKey, r.Kind, r.Key)
	}, nil
}

// ───────────────────────────────────────────────────────────── KV

const (
	maxKVKey   = 512
	maxKVValue = 1 << 20
	// maxKVListBytes caps the keys and values of one kv.list page (the page is cut short and
	// its cursor continues after the last item).
	maxKVListBytes = 4 << 20
)

type kvArgs struct {
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value"`
	TTLSeconds float64         `json:"ttlSeconds"`
	By         *float64        `json:"by"`
	Prefix     string          `json:"prefix"`
	Limit      int             `json:"limit"`
	Cursor     string          `json:"cursor"`
}

func kvOp(i *Instance, args json.RawMessage, needKey bool) (kvArgs, error) {
	if err := i.require(capKV); err != nil {
		return kvArgs{}, err
	}
	if i.env.KV == nil {
		return kvArgs{}, errors.New("kv: storage is not available for this instance")
	}
	a, err := decode[kvArgs](args)
	if err != nil {
		return a, err
	}
	if needKey && (a.Key == "" || len(a.Key) > maxKVKey) {
		return a, argErr("kv: key must be 1-%d bytes", maxKVKey)
	}
	return a, nil
}

func opKVGet(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := kvOp(i, args, true)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		v, err := i.env.KV.Get(ctx, a.Key)
		if errors.Is(err, state.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return v, nil
	}, nil
}

func opKVSet(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := kvOp(i, args, true)
	if err != nil {
		return nil, err
	}
	if len(a.Value) == 0 {
		return nil, argErr("kv.set: value is required")
	}
	if len(a.Value) > maxKVValue {
		return nil, argErr("kv.set: value larger than %d bytes", maxKVValue)
	}
	if a.TTLSeconds < 0 {
		return nil, argErr("kv.set: ttlSeconds must be positive")
	}
	ttl := time.Duration(a.TTLSeconds * float64(time.Second))
	return func(ctx context.Context) (any, error) {
		return nil, i.env.KV.Set(ctx, a.Key, a.Value, ttl)
	}, nil
}

func opKVDelete(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := kvOp(i, args, true)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (any, error) {
		err := i.env.KV.Delete(ctx, a.Key)
		if errors.Is(err, state.ErrNotFound) {
			err = nil
		}
		return nil, err
	}, nil
}

func opKVIncrement(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := kvOp(i, args, true)
	if err != nil {
		return nil, err
	}
	by := 1.0
	if a.By != nil {
		by = *a.By
	}
	return func(ctx context.Context) (any, error) {
		return i.env.KV.Increment(ctx, a.Key, by)
	}, nil
}

type kvItemDTO struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

func opKVList(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	a, err := kvOp(i, args, false)
	if err != nil {
		return nil, err
	}
	if a.Limit == 0 {
		a.Limit = 100
	}
	if a.Limit < 1 || a.Limit > 1000 {
		return nil, argErr("kv.list: limit must be between 1 and 1000")
	}
	return func(ctx context.Context) (any, error) {
		items, next, err := i.env.KV.List(ctx, a.Prefix, a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		out := struct {
			Items  []kvItemDTO `json:"items"`
			Cursor string      `json:"cursor,omitempty"`
		}{Items: make([]kvItemDTO, 0, len(items)), Cursor: next}
		size := 0
		for _, it := range items {
			size += len(it.Key) + len(it.Value)
			if size > maxKVListBytes && len(out.Items) > 0 {
				// Cut the page; the next one starts after the last item returned.
				out.Cursor = out.Items[len(out.Items)-1].Key
				break
			}
			out.Items = append(out.Items, kvItemDTO{Key: it.Key, Value: it.Value})
		}
		return out, nil
	}, nil
}

// ───────────────────────────────────────────────────────────── scheduler

func opScheduleAfter(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capScheduler); err != nil {
		return nil, err
	}
	if i.env.Tasks == nil {
		return nil, errors.New("schedule: the task queue is not available for this instance")
	}
	a, err := decode[struct {
		DelayMs float64         `json:"delayMs"`
		Task    string          `json:"task"`
		Payload json.RawMessage `json:"payload"`
	}](args)
	if err != nil {
		return nil, err
	}
	if a.DelayMs != a.DelayMs || a.DelayMs < 0 {
		return nil, argErr("schedule.after: delayMs must be a positive number")
	}
	if def := i.Definition(); def == nil || !def.HasTask(a.Task) {
		return nil, argErr("schedule.after: unknown task %q (declare it under tasks)", a.Task)
	}
	if len(a.Payload) > maxKVValue {
		return nil, argErr("schedule.after: payload larger than %d bytes", maxKVValue)
	}
	if string(a.Payload) == "null" {
		a.Payload = nil
	}
	return func(ctx context.Context) (any, error) {
		t := state.Task{
			ID: newID(), Scope: i.env.TaskScope, Name: a.Task, Payload: a.Payload,
			RunAt: time.Now().UTC().Add(time.Duration(a.DelayMs * float64(time.Millisecond))),
		}
		if err := i.env.Tasks.Schedule(ctx, t); err != nil {
			return nil, err
		}
		return map[string]string{"id": t.ID}, nil
	}, nil
}

func opScheduleCancel(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capScheduler); err != nil {
		return nil, err
	}
	if i.env.Tasks == nil {
		return nil, errors.New("schedule: the task queue is not available for this instance")
	}
	a, err := decode[struct {
		ID string `json:"id"`
	}](args)
	if err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, argErr("schedule.cancel: id is required")
	}
	return func(ctx context.Context) (any, error) {
		err := i.env.Tasks.Cancel(ctx, i.env.TaskScope, a.ID)
		if errors.Is(err, state.ErrNotFound) {
			err = nil
		}
		return nil, err
	}, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ───────────────────────────────────────────────────────────── events & portal

func opEventsPublish(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capEventsPublish); err != nil {
		return nil, err
	}
	if i.env.Publish == nil {
		return nil, errors.New("events.publish: the event bus is not available")
	}
	a, err := decode[struct {
		Topic   string          `json:"topic"`
		Payload json.RawMessage `json:"payload"`
	}](args)
	if err != nil {
		return nil, err
	}
	if !topicRe.MatchString(a.Topic) {
		return nil, argErr("events.publish: invalid topic %q (letters, digits, . _ : -; at most 100)", a.Topic)
	}
	if len(a.Payload) == 0 {
		a.Payload = json.RawMessage("null")
	}
	if len(a.Payload) > maxKVValue {
		return nil, argErr("events.publish: payload larger than %d bytes", maxKVValue)
	}
	topic := i.env.PluginName + "." + a.Topic
	return func(context.Context) (any, error) {
		return nil, i.env.Publish(topic, a.Payload)
	}, nil
}

func opPortalLink(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capPortalLinks); err != nil {
		return nil, err
	}
	if i.env.Portal == nil {
		return nil, errors.New("portal.link: no community portal is configured")
	}
	a, err := decode[struct {
		Scope            *auth.ResourceScope `json:"scope"`
		Path             string              `json:"path"`
		UserID           string              `json:"userId"`
		ExpiresInSeconds float64             `json:"expiresInSeconds"`
	}](args)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(a.Path, "/") {
		return nil, argErr("portal.link: path must start with /")
	}
	if a.UserID != "" {
		if err := checkSnowflake("user", a.UserID); err != nil {
			return nil, err
		}
	}
	if a.ExpiresInSeconds < 0 {
		return nil, argErr("portal.link: expiresInSeconds must be positive")
	}
	ttl := time.Duration(a.ExpiresInSeconds * float64(time.Second))
	if a.Scope != nil {
		scoped, ok := i.env.Portal.(scopedLinker)
		if !ok {
			return nil, errors.New("scoped links unavailable")
		}
		if a.ExpiresInSeconds > 7*24*3600 {
			return nil, argErr("scoped link lifetime exceeds seven days")
		}
		return func(ctx context.Context) (any, error) { return scoped.ScopedLink(ctx, a.Path, a.UserID, ttl, *a.Scope) }, nil
	}
	return func(context.Context) (any, error) {
		return i.env.Portal.Link(a.Path, a.UserID, ttl)
	}, nil
}

type scopedLinker interface {
	ScopedLink(context.Context, string, string, time.Duration, auth.ResourceScope) (string, error)
	Revoke(context.Context, auth.ResourceScope) error
}

func opPortalRevoke(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capPortalLinks); err != nil {
		return nil, err
	}
	scope, err := decode[auth.ResourceScope](args)
	if err != nil {
		return nil, err
	}
	l, ok := i.env.Portal.(scopedLinker)
	if !ok {
		return nil, errors.New("scoped links unavailable")
	}
	return func(ctx context.Context) (any, error) { return nil, l.Revoke(ctx, scope) }, nil
}
