package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/plugin"
	"github.com/cicord/cicord/internal/state"
)

// botHost is one connected bot identity: a session plus the routing table of the
// active revision, swapped atomically.
type botHost struct {
	rt        *Runtime
	key       string
	tokenHash string
	intents   discord.Intents
	session   discord.Session

	routing atomic.Pointer[routing]

	cmdMu       sync.Mutex
	cmdHashes   map[string]string // serverID → hash of the last command set
	profileHash string
	presence    string
}

type routing struct {
	byServer    map[string][]*instanceRT          // serverID → instances
	byName      map[string]map[string]*instanceRT // instance name → serverID → instance
	commands    map[string]map[string]*instanceRT // serverID → command → instance
	reconcilers map[string]*serverRT              // serverID → server (when this bot reconciles it)
	caches      map[string]*serverCache           // serverID → cache
}

func (r *Runtime) botError(key string) string {
	if v, ok := r.botErrs.Load(key); ok {
		return v.(string)
	}
	return "not connected"
}

// enabledIntents returns the privileged intents enabled for a bot's application, when
// known (config.LoadOptions.PrivilegedIntents).
func (r *Runtime) enabledIntents(bot string) (discord.Intents, bool) {
	v, ok := r.appIntents.Load(bot)
	if !ok {
		return 0, false
	}
	return v.(discord.Intents), true
}

func (r *Runtime) bot(key string) *botHost {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.bots[key]
}

func (r *Runtime) hosts() []*botHost {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*botHost, 0, len(r.bots))
	for _, k := range sortedKeys(r.bots) {
		out = append(out, r.bots[k])
	}
	return out
}

// reconcilingBot returns the first connected bot (by key) that lists the server.
func (r *Runtime) reconcilingBot(repo *config.Repo, server string) *botHost {
	for _, key := range sortedKeys(repo.Bots) {
		for _, s := range repo.Bots[key].Spec.Servers {
			if s == server {
				if h := r.bot(key); h != nil {
					return h
				}
			}
		}
	}
	return nil
}

// botIntents are the gateway intents requested for a bot (config.Repo.GatewayIntents).
func botIntents(repo *config.Repo, key string) discord.Intents {
	intents, _ := discord.ParseIntents(repo.GatewayIntents(key)) // validated by config.Load
	return intents
}

// connectBots (re)connects bot sessions to match the repository. Sessions whose token
// and intents are unchanged are kept (no gateway reconnect on a new revision).
func (r *Runtime) connectBots(ctx context.Context, repo *config.Repo) {
	wanted := map[string]bool{}
	for _, key := range sortedKeys(repo.Bots) {
		b := repo.Bots[key]
		wanted[key] = true
		token, ok := r.opts.Secrets(b.Spec.Token.SecretRef)
		if !ok || strings.TrimSpace(token) == "" {
			r.botErrs.Store(key, fmt.Sprintf("secret %s is not set", b.Spec.Token.SecretRef))
			r.dropBot(key)
			continue
		}
		sum := sha256.Sum256([]byte(token))
		th := hex.EncodeToString(sum[:])
		intents := botIntents(repo, key)
		if h := r.bot(key); h != nil && h.tokenHash == th && h.intents == intents {
			continue
		}
		r.dropBot(key)
		sess, err := r.opts.Connector.Connect(ctx, token)
		if err != nil {
			r.botErrs.Store(key, fmt.Sprintf("connect: %v", err))
			r.log.Error("bot connect failed", "bot", key, "err", err)
			continue
		}
		if sess.ApplicationID() != b.Spec.ApplicationID {
			r.botErrs.Store(key, fmt.Sprintf("token belongs to application %s, bot.yaml says %s", sess.ApplicationID(), b.Spec.ApplicationID))
			_ = sess.Close(ctx)
			continue
		}
		if fr, ok := sess.(discord.ApplicationFlagsReader); ok {
			enabled := discord.IntentsFromFlags(fr.ApplicationFlags())
			r.appIntents.Store(key, enabled)
			if missing := intents & discord.PrivilegedIntents &^ enabled; missing != 0 {
				// Discord would close the gateway with 4014: say which toggle to turn on.
				msg := fmt.Sprintf("the privileged intent(s) %s are not enabled for the application: turn them on in the Discord developer portal (%s → Privileged Gateway Intents), then restart Cicord or push a new revision",
					strings.Join(missing.Names(), ", "), discord.PortalBotURL(b.Spec.ApplicationID))
				r.botErrs.Store(key, msg)
				r.log.Error("bot privileged intents not enabled", "bot", key, "missing", strings.Join(missing.Names(), ","), "portal", discord.PortalBotURL(b.Spec.ApplicationID))
				_ = sess.Close(ctx)
				continue
			}
		} else {
			r.appIntents.Delete(key)
		}
		h := &botHost{rt: r, key: key, tokenHash: th, intents: intents, session: sess, cmdHashes: map[string]string{}}
		h.routing.Store(&routing{})
		if p := b.Spec.Presence; p != nil {
			_ = sess.SetPresence(ctx, toPresence(p))
		}
		if err := sess.Open(ctx, intents, h.handle); err != nil {
			r.botErrs.Store(key, fmt.Sprintf("gateway: %v", err))
			r.log.Error("bot gateway failed", "bot", key, "err", err)
			_ = sess.Close(ctx)
			continue
		}
		r.botErrs.Delete(key)
		r.mu.Lock()
		r.bots[key] = h
		r.mu.Unlock()
		r.log.Info("bot connected", "bot", key, "user", sess.Self().Username, "intents", strings.Join(intents.Names(), ","))
	}
	for _, h := range r.hosts() {
		if !wanted[h.key] {
			r.dropBot(h.key)
		}
	}
}

func (r *Runtime) dropBot(key string) {
	r.mu.Lock()
	h := r.bots[key]
	delete(r.bots, key)
	r.mu.Unlock()
	if h != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.session.Close(ctx)
	}
}

func toPresence(p *config.Presence) discord.Presence {
	out := discord.Presence{Status: p.Status}
	if out.Status == "" {
		out.Status = "online"
	}
	if p.Activity != nil {
		out.ActivityType, out.ActivityName, out.ActivityURL = p.Activity.Type, p.Activity.Name, p.Activity.URL
	}
	return out
}

func instanceScope(in config.Instance) string { return "plugin:" + in.ID() }

func (r *Runtime) startInstance(ctx context.Context, host *botHost, srt *serverRT, irt *instanceRT) error {
	if irt.plugin == nil {
		return errors.New("plugin failed to build")
	}
	in := irt.cfg
	m := in.Plugin.Manifest
	var network []string
	if m.Spec.Network != nil {
		network = m.Spec.Network.Allow
	}
	srvName := srt.cache.Server().Name
	env := plugin.Env{
		BotKey: in.Bot, InstanceName: in.Name, PluginName: m.Metadata.Name, PluginVersion: m.Metadata.Version,
		Bot: host.session.Self(), ApplicationID: host.session.ApplicationID(),
		ServerKey: in.Server, ServerID: srt.cfg.Spec.DiscordID, ServerName: srvName, ServerIconURL: srt.cache.Server().IconURL,
		Config: in.Config, Grants: in.Grants, NetworkAllow: network, AllowPrivateNetworks: r.opts.AllowPrivateNetworks,
		MessageContent: slices.Contains(m.Spec.Intents, "MessageContent"), HTTPClient: r.opts.PluginHTTPClient,
		Discord: host.session, Resolver: srt.cache,
		KV: r.opts.Store.KV(instanceScope(in)), Tasks: r.opts.Store.Tasks(), TaskScope: instanceScope(in),
		Resources: ownedResources{ResourceMap: r.opts.Store.Resources(), cache: srt.cache},
		Portal:    &auth.Linker{Signer: r.opts.Signer, BaseURL: r.opts.PortalURL, Server: in.Server, MaxTTL: 24 * time.Hour},
		Publish: func(topic string, payload json.RawMessage) error {
			r.publish(in.Bot, srt.cfg.Spec.DiscordID, topic, payload)
			return nil
		},
		Logger: r.log.With("bot", in.Bot, "instance", in.Name, "server", in.Server),
		Limits: r.opts.PluginLimits,
	}
	inst, err := plugin.NewInstance(irt.plugin.code, irt.plugin.def, env)
	if err != nil {
		return err
	}
	if err := inst.Start(ctx); err != nil {
		_ = inst.Stop(ctx)
		return err
	}
	irt.inst = inst
	return nil
}

// ownedResources records plugin-owned resources in both the store and the cache.
type ownedResources struct {
	state.ResourceMap
	cache *serverCache
}

func (o ownedResources) Put(ctx context.Context, res state.Resource) error {
	if err := o.ResourceMap.Put(ctx, res); err != nil {
		return err
	}
	o.cache.Remember(res.Kind, res.Key, res.ID)
	return nil
}

// install builds the routing table for this bot from the new revision, syncs its slash
// commands per server and applies profile changes.
func (h *botHost) install(ctx context.Context, a *active) {
	rt := &routing{
		byServer: map[string][]*instanceRT{}, byName: map[string]map[string]*instanceRT{},
		commands: map[string]map[string]*instanceRT{}, reconcilers: map[string]*serverRT{}, caches: map[string]*serverCache{},
	}
	b := a.repo.Bots[h.key]
	if b == nil {
		h.routing.Store(rt)
		return
	}
	for _, sk := range b.Spec.Servers {
		if s := a.servers[sk]; s != nil {
			rt.caches[s.cfg.Spec.DiscordID] = s.cache
			if s.bot == h.key {
				rt.reconcilers[s.cfg.Spec.DiscordID] = s
			}
		}
	}
	for _, i := range a.instances {
		if i.cfg.Bot != h.key || i.inst == nil {
			continue
		}
		sid := i.serverID
		rt.byServer[sid] = append(rt.byServer[sid], i)
		if rt.byName[i.cfg.Name] == nil {
			rt.byName[i.cfg.Name] = map[string]*instanceRT{}
		}
		rt.byName[i.cfg.Name][sid] = i
		for _, c := range i.plugin.def.Commands {
			if rt.commands[sid] == nil {
				rt.commands[sid] = map[string]*instanceRT{}
			}
			rt.commands[sid][c.Name] = i
		}
	}
	h.routing.Store(rt)

	// slash commands per server
	for _, sk := range b.Spec.Servers {
		s := a.servers[sk]
		if s == nil {
			continue
		}
		sid := s.cfg.Spec.DiscordID
		var cmds []discord.Command
		for _, i := range rt.byServer[sid] {
			cmds = append(cmds, i.plugin.def.DiscordCommands()...)
		}
		sort.Slice(cmds, func(x, y int) bool { return cmds[x].Name < cmds[y].Name })
		data, _ := json.Marshal(cmds)
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		h.cmdMu.Lock()
		same := h.cmdHashes[sid] == hash
		h.cmdMu.Unlock()
		if same {
			continue
		}
		if cmds == nil {
			cmds = []discord.Command{}
		}
		if err := h.session.SetServerCommands(ctx, h.session.ApplicationID(), sid, cmds); err != nil {
			h.rt.diag.recordCommands(h.key, sid, len(cmds), err)
			h.rt.log.Error("sync slash commands", "bot", h.key, "server", sk, "err", err)
			continue
		}
		h.rt.diag.recordCommands(h.key, sid, len(cmds), nil)
		h.cmdMu.Lock()
		h.cmdHashes[sid] = hash
		h.cmdMu.Unlock()
	}

	// profile
	if p := b.Spec.Profile; p != nil {
		var sp discord.SelfParams
		sig := p.Username
		if p.Username != "" && p.Username != h.session.Self().Username {
			u := p.Username
			sp.Username = &u
		}
		if p.Avatar != "" {
			data, err := os.ReadFile(filepath.Join(a.repo.Root, filepath.Dir(b.File), filepath.FromSlash(p.Avatar)))
			if err == nil {
				sum := sha256.Sum256(data)
				sig += hex.EncodeToString(sum[:])
				if h.profileHash != sig {
					sp.Avatar = &data
				}
			}
		}
		if (sp.Username != nil || sp.Avatar != nil) && h.profileHash != sig {
			if _, err := h.session.UpdateSelf(ctx, sp); err != nil {
				h.rt.log.Warn("update bot profile", "bot", h.key, "err", err)
			} else {
				h.profileHash = sig
			}
		}
	}
	if p := b.Spec.Presence; p != nil {
		ps := toPresence(p)
		sig := ps.Status + "|" + ps.ActivityType + "|" + ps.ActivityName
		if sig != h.presence {
			if err := h.session.SetPresence(ctx, ps); err == nil {
				h.presence = sig
			}
		}
	}
}

// handle is the gateway event handler (called sequentially by the session).
func (h *botHost) handle(ev discord.Event) {
	h.rt.diag.observe(ev)
	rt := h.routing.Load()
	sid := discord.ServerIDOf(ev)
	if c := rt.caches[sid]; c != nil {
		c.Observe(ev)
	}
	if s := rt.reconcilers[sid]; s != nil && s.rec != nil {
		s.rec.HandleEvent(ev)
	}
	switch e := ev.(type) {
	case discord.InteractionCreateEvent:
		h.routeInteraction(rt, e.Interaction)
		return
	case discord.MessageCreateEvent:
		if e.Message.Author.ID == h.session.Self().ID {
			return // never feed a bot its own messages (loops)
		}
	case discord.MessageUpdateEvent:
		if e.Message.Author.ID == h.session.Self().ID {
			return
		}
	}
	name := plugin.EventName(ev)
	if name == "" {
		return
	}
	for _, i := range rt.byServer[sid] {
		if !i.plugin.def.HasEvent(name) {
			continue
		}
		done := i.inst.DispatchEvent(context.Background(), ev)
		go func() {
			err := <-done
			i.setHealth(err)
			if err != nil {
				h.rt.log.Warn("plugin event handler failed", "instance", i.id(), "event", name, "err", err)
			}
		}()
	}
}

func (h *botHost) routeInteraction(rt *routing, it discord.Interaction) {
	var (
		target  *instanceRT
		kind    string
		localID string
	)
	switch it.Type {
	case discord.InteractionCommand:
		target, kind, localID = rt.commands[it.ServerID][it.CommandName], "command", it.CommandName
	case discord.InteractionComponent, discord.InteractionModalSubmit:
		name, local, ok := plugin.ParseCustomID(it.CustomID)
		if !ok {
			return // not a Cicord component (another app's message?)
		}
		kind, localID = "component", local
		if it.Type == discord.InteractionModalSubmit {
			kind = "modal"
		}
		if byServer := rt.byName[name]; byServer != nil {
			target = byServer[it.ServerID]
			if target == nil && it.ServerID == "" && len(byServer) == 1 {
				for _, only := range byServer { // DM interaction: the instance is unambiguous
					target = only
				}
			}
		}
	default:
		return
	}
	if target == nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = h.session.RespondInteraction(ctx, it.ID, it.Token, discord.InteractionResponse{
				Type: discord.ResponseMessage,
				Data: &discord.MessageCreate{Content: "This action is no longer available.", Flags: discord.MessageFlagEphemeral},
			})
		}()
		return
	}
	done := target.inst.DispatchInteraction(context.Background(), it, kind, localID)
	go func() {
		err := <-done
		target.setHealth(err)
		if err != nil {
			h.rt.log.Warn("plugin interaction failed", "instance", target.id(), "kind", kind, "id", localID, "err", err)
		}
	}()
}

// publish delivers a cross-plugin event to subscribers on the same (bot, server).
func (r *Runtime) publish(bot, serverID, topic string, payload json.RawMessage) {
	h := r.bot(bot)
	if h == nil {
		return
	}
	for _, i := range h.routing.Load().byServer[serverID] {
		for _, sub := range i.plugin.def.Subscriptions {
			if sub == topic {
				i := i
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					err := i.inst.Deliver(ctx, topic, payload)
					i.setHealth(err)
				}()
			}
		}
	}
}
