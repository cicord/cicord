package runtime

import (
	"sync"
	"time"

	"github.com/npjonath/cicord/internal/discord"
)

// diagnostics records what the setup wizard and the Diagnostics page check after the
// fact: gateway messages seen per server and the last slash-command registration result
// per (bot, server).
type diagnostics struct {
	lastMessage sync.Map // server ID → time.Time
	commands    sync.Map // bot + "/" + server ID → CommandSync
}

// CommandSync is the last slash-command registration of a bot in a server.
type CommandSync struct {
	At       time.Time
	Commands int
	Err      string
}

func (d *diagnostics) observe(ev discord.Event) {
	if e, ok := ev.(discord.MessageCreateEvent); ok && e.Message.ServerID != "" && !e.Message.Author.Bot {
		d.lastMessage.Store(e.Message.ServerID, time.Now())
	}
}

func (d *diagnostics) recordCommands(bot, serverID string, n int, err error) {
	cs := CommandSync{At: time.Now(), Commands: n}
	if err != nil {
		cs.Err = err.Error()
	}
	d.commands.Store(bot+"/"+serverID, cs)
}

// LastMessageAt returns when a member's message was last received through the gateway
// in a server (by any connected bot).
func (r *Runtime) LastMessageAt(serverID string) (time.Time, bool) {
	v, ok := r.diag.lastMessage.Load(serverID)
	if !ok {
		return time.Time{}, false
	}
	return v.(time.Time), true
}

// CommandSyncStatus returns the last slash-command registration of a bot in a server.
func (r *Runtime) CommandSyncStatus(bot, serverID string) (CommandSync, bool) {
	v, ok := r.diag.commands.Load(bot + "/" + serverID)
	if !ok {
		return CommandSync{}, false
	}
	return v.(CommandSync), true
}

// BotSession returns the session and gateway intents of a connected bot.
func (r *Runtime) BotSession(key string) (discord.Session, discord.Intents, bool) {
	h := r.bot(key)
	if h == nil {
		return nil, 0, false
	}
	return h.session, h.intents, true
}

// BotError returns the last connection error of a bot ("" when connected).
func (r *Runtime) BotError(key string) string {
	if r.bot(key) != nil {
		return ""
	}
	return r.botError(key)
}
