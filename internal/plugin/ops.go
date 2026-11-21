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
	"sort"
	"strings"
	"time"

	"github.com/grafana/sobek"

	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/state"
)

// Capabilities (spec/README.md §6).
const (
	capMessagesRead    = "discord.messages.read"
	capMessagesSend    = "discord.messages.send"
	capMessagesManage  = "discord.messages.manage"
	capReactions       = "discord.reactions.manage"
	capMembersRead     = "discord.members.read"
	capMembersManage   = "discord.members.manage"
	capMembersModerate = "discord.members.moderate"
	capRolesAssign     = "discord.roles.assign"
	capChannelsRead    = "discord.channels.read"
	capChannelsManage  = "discord.channels.manage"
	capThreadsManage   = "discord.threads.manage"
	capDMSend          = "discord.dm.send"
	capKV              = "storage.kv"
	capScheduler       = "scheduler"
	capHTTPFetch       = "http.fetch"
	capPortalRoutes    = "portal.routes"
	capPortalLinks     = "portal.links"
	capEventsPublish   = "events.publish"
	capEventsSubscribe = "events.subscribe"
)

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
		"resolve.role":   opResolveRole,
		"server.channel": opServerChannel,
		"server.role":    opServerRole,
		"it.state":       opInteractionState,
	}
	asyncOps = map[string]asyncOp{
		"channel.send":         opChannelSend,
		"channel.messages":     opChannelMessages,
		"channel.createThread": opChannelCreateThread,
		"message.reply":        opMessageReply,
		"message.edit":         opMessageEdit,
		"message.delete":       opMessageDelete,
		"message.react":        opMessageReact,
		"member.addRole":       opMemberRole(true),
		"member.removeRole":    opMemberRole(false),
		"member.setNickname":   opMemberSetNickname,
		"member.timeout":       opMemberTimeout,
		"member.kick":          opMemberKick,
		"member.ban":           opMemberBan,
		"member.send":          opMemberSend,
		"server.member":        opServerMember,
		"server.createChannel": opServerCreateChannel,
		"server.deleteChannel": opServerDeleteChannel,
		"kv.get":               opKVGet,
		"kv.set":               opKVSet,
		"kv.delete":            opKVDelete,
		"kv.increment":         opKVIncrement,
		"kv.list":              opKVList,
		"schedule.after":       opScheduleAfter,
		"schedule.cancel":      opScheduleCancel,
		"http.fetch":           opHTTPFetch,
		"events.publish":       opEventsPublish,
		"portal.link":          opPortalLink,
		"it.reply":             opInteractionReply,
		"it.defer":             opInteractionDefer,
		"it.followUp":          opInteractionFollowUp,
		"it.showModal":         opInteractionShowModal,
		"it.update":            opInteractionUpdate,
	}
}

func (i *Instance) call(op, args string) (sobek.Value, error) {
	f, ok := syncOps[op]
	if !ok {
		return nil, fmt.Errorf("unknown host call %q", op)
	}
	res, err := f(i, i.currentInv(), json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	return i.jsonValue(res)
}

func (i *Instance) callAsync(op, args string) (sobek.Value, error) {
	f, ok := asyncOps[op]
	if !ok {
		return nil, fmt.Errorf("unknown host call %q", op)
	}
	inv := i.currentInv()
	if err := inv.alive(); err != nil {
		return nil, err
	}
	work, err := f(i, inv, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	p, resolve, reject := i.vm.rt.NewPromise()
	go func() {
		res, err := work(inv.ctx)
		var data []byte
		if err == nil && res != nil {
			data, err = json.Marshal(res)
		}
		if err != nil && inv.ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			err = inv.alive()
		}
		i.post(func() {
			rerr := i.enter(inv, i.limits.SyncBudget, func() error {
				if err != nil {
					return reject(i.vm.jsError(err))
				}
				if data == nil {
					return resolve(sobek.Undefined())
				}
				return resolve(string(data))
			})
			if rerr != nil && !isInterrupt(rerr) {
				i.logger.Error("plugin host: settle host call", "op", op, "error", rerr)
			}
		})
	}()
	return i.vm.rt.ToValue(p), nil
}

func (i *Instance) jsonValue(res any) (sobek.Value, error) {
	if res == nil {
		return sobek.Undefined(), nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	return i.vm.rt.ToValue(string(b)), nil
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
		m, err := i.env.Discord.SendMessage(ctx, a.ChannelID, mc)
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
		m, err := i.env.Discord.SendMessage(ctx, a.ChannelID, mc)
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
	return func(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
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
			return i.roleDTO(roleID), nil
		}, nil
	}
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
		m, err := i.env.Discord.SendMessage(ctx, ch.ID, mc)
		if err != nil {
			return nil, err
		}
		return i.messageDTO(m), nil
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

// owner is the ResourceMap owner of plugin-created resources.
func (i *Instance) owner() string { return "plugin:" + i.env.BotKey + "/" + i.env.InstanceName }

func opServerCreateChannel(i *Instance, _ *invocation, args json.RawMessage) (func(context.Context) (any, error), error) {
	if err := i.require(capChannelsManage); err != nil {
		return nil, err
	}
	a, err := decode[struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		Category   ref    `json:"category"`
		Topic      string `json:"topic"`
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
	if len(a.Overwrites) > 0 {
		targets := make([]string, 0, len(a.Overwrites))
		for t := range a.Overwrites {
			targets = append(targets, t)
		}
		sort.Strings(targets)
		ows := make([]discord.Overwrite, 0, len(targets))
		for _, target := range targets {
			spec := a.Overwrites[target]
			ow := discord.Overwrite{Type: discord.OverwriteRole}
			switch {
			case target == "@everyone":
				ow.ID = i.env.ServerID
			case strings.HasPrefix(target, "member:"):
				ow.ID, ow.Type = strings.TrimPrefix(target, "member:"), discord.OverwriteMember
				if err := checkSnowflake("member", ow.ID); err != nil {
					return nil, err
				}
			case strings.HasPrefix(target, "role:"):
				ow.ID = strings.TrimPrefix(target, "role:")
				if err := checkSnowflake("role", ow.ID); err != nil {
					return nil, err
				}
			default:
				id, err := i.resolve(state.KindRole, ref{Key: target, set: true})
				if err != nil {
					return nil, fmt.Errorf("createChannel(): overwrite target %q: %w (use @everyone, a role key, role:<id> or member:<id>)", target, err)
				}
				ow.ID = id
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
		ch, err := i.env.Discord.CreateChannel(ctx, i.env.ServerID, p, "codi: created by plugin "+i.env.PluginName+" ("+i.env.InstanceName+")")
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
		r, err := i.env.Resources.GetByID(ctx, i.env.ServerKey, id)
		if errors.Is(err, state.ErrNotFound) || (err == nil && r.Owner != i.owner()) {
			return nil, fmt.Errorf("deleteChannel(): channel %s is not owned by this plugin instance", id)
		}
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
		}{Items: make([]kvItemDTO, len(items)), Cursor: next}
		for k, it := range items {
			out.Items[k] = kvItemDTO{Key: it.Key, Value: it.Value}
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
		Path             string  `json:"path"`
		UserID           string  `json:"userId"`
		ExpiresInSeconds float64 `json:"expiresInSeconds"`
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
	return func(context.Context) (any, error) {
		return i.env.Portal.Link(a.Path, a.UserID, ttl)
	}, nil
}
