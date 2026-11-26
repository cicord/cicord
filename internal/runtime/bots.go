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
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/state"
)

// botHost is one connected bot identity: a session plus the routing table of the
// active revision, swapped atomically.
type botHost struct {
	dm        directMessages
	rt        *Runtime
	key       string
	tokenHash string
	intents   discord.Intents
	session   discord.Session
	// voice is the session's voice state cache (server.voiceMembers, voiceStateUpdate's
	// previous channel).
	voice *voiceCache

	routing atomic.Pointer[routing]

	cmdMu              sync.Mutex
	cmdHashes          map[string]string // serverID → hash of the last command set
	cmdSync            sync.Mutex        // serializes SetServerCommands calls
	profileHash        string
	guildProfileMu     sync.Mutex
	guildProfileHashes map[string][32]byte
	presence           string
}

type routing struct {
	dmOwner, dmLocale        string
	guildProfiles            map[string]config.BotServer
	profileRoot, profileFile string
	byServer                 map[string][]*instanceRT          // serverID → instances
	byName                   map[string]map[string]*instanceRT // instance name → serverID → instance
	commands                 map[string]map[string]*instanceRT // serverID → command → instance
	reconcilers              map[string]*serverRT              // serverID → server (when this bot reconciles it)
	caches                   map[string]*serverCache           // serverID → cache
	serverKeys               map[string]string                 // serverID → server key, for the servers of the bot's spec
}

// syncCommands registers the slash commands of the instances of a server, unless the same
// set was registered last time (cmdHashes).
func (h *botHost) syncCommands(ctx context.Context, rt *routing, sid string) {
	h.cmdSync.Lock() // one registration at a time (activation, server joins)
	defer h.cmdSync.Unlock()
	var cmds []discord.Command
	for _, i := range rt.byServer[sid] {
		cmds = append(cmds, i.plugin.def.DiscordCommands()...)
	}
	sort.Slice(cmds, func(x, y int) bool { return cmds[x].Type.Key(cmds[x].Name) < cmds[y].Type.Key(cmds[y].Name) })
	data, _ := json.Marshal(cmds)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	h.cmdMu.Lock()
	same := h.cmdHashes[sid] == hash
	h.cmdMu.Unlock()
	if same {
		return
	}
	if cmds == nil {
		cmds = []discord.Command{}
	}
	if err := h.session.SetServerCommands(ctx, h.session.ApplicationID(), sid, cmds); err != nil {
		h.rt.diag.recordCommands(h.key, sid, len(cmds), err)
		h.rt.log.Error("sync slash commands", "bot", h.key, "server", rt.serverKeys[sid], "err", err)
		return
	}
	h.rt.diag.recordCommands(h.key, sid, len(cmds), nil)
	h.cmdMu.Lock()
	h.cmdHashes[sid] = hash
	h.cmdMu.Unlock()
}

// serverMembership reacts to the bot joining or leaving a server. Discord drops the
// server commands of an application removed from a server, so after a re-invite they are
// registered again; a leave forgets the last registered set.
func (h *botHost) serverMembership(rt *routing, ev discord.Event) {
	switch e := ev.(type) {
	case discord.ServerJoinEvent:
		sid := e.Server.ID
		if _, ok := rt.serverKeys[sid]; !ok {
			return // not a server of this bot's spec: nothing to register
		}
		h.cmdMu.Lock()
		delete(h.cmdHashes, sid)
		h.cmdMu.Unlock()
		h.rt.log.Info("bot joined a server", "bot", h.key, "server", rt.serverKeys[sid])
		go func() { // the gateway handler must not block on REST
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			current := h.routing.Load()
			h.syncCommands(ctx, current, sid)
			h.applyGuildProfile(ctx, current, sid, true)
		}()
	case discord.ServerLeaveEvent:
		h.cmdMu.Lock()
		delete(h.cmdHashes, e.ServerID)
		h.cmdMu.Unlock()
		if sk, ok := rt.serverKeys[e.ServerID]; ok {
			h.rt.log.Warn("bot left a server of its spec", "bot", h.key, "server", sk)
		}
	}
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
		for _, s := range repo.Bots[key].Spec.ServerKeys() {
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
		intents := botIntents(repo, key) | r.analyticsIntents(repo, key)
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
			repo.ApplyOptionalIntents(key, enabled)
			intents = botIntents(repo, key) | r.analyticsIntents(repo, key)
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
		h := &botHost{rt: r, key: key, tokenHash: th, intents: intents, session: sess, cmdHashes: map[string]string{},
			voice: newVoiceCache(intents)}
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
	r.notify(ChangeStatus, "")
}

func (r *Runtime) dropBot(key string) {
	r.mu.Lock()
	h := r.bots[key]
	delete(r.bots, key)
	r.mu.Unlock()
	if h != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.stopDM()
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

// pluginAccess gives plugins the server's access levels (Member.access).
func pluginAccess(a *config.Access) plugin.Access {
	var out plugin.Access
	if a == nil {
		return out
	}
	if p := a.Admins; p != nil {
		out.AdminUsers, out.AdminRoles = p.Users, p.Roles
	}
	if p := a.Moderators; p != nil {
		out.ModeratorUsers, out.ModeratorRoles = p.Users, p.Roles
	}
	return out
}

// startInstance creates and starts a plugin instance for irt (the caller installs it).
func (r *Runtime) startInstance(ctx context.Context, host *botHost, srt *serverRT, irt *instanceRT) (*plugin.Instance, error) {
	if irt.plugin == nil {
		return nil, errors.New("plugin failed to build")
	}
	in := irt.cfg
	m := in.Plugin.Manifest

	srvName := srt.cache.Server().Name
	declaredRolesByUser := map[string][]string{}
	var declaredMemberRoles []string
	for key, role := range srt.cfg.Spec.Roles {
		for _, user := range role.Members {
			declaredRolesByUser[user] = append(declaredRolesByUser[user], key)
		}
		if role.Members != nil {
			declaredMemberRoles = append(declaredMemberRoles, key)
		}
	}
	origin := in.Bot + "/" + in.Name
	budgeted := map[string]bool{}
	for _, s := range m.Spec.Secrets {
		if s.Budget != nil {
			budgeted[s.Name] = true
		}
	}
	env := plugin.Env{
		PrivacyFence: r.privacyFence(in.Server),
		Locales:      in.Plugin.Locales,
		OverlayApply: func(c context.Context, o reconcile.Overlay) error {
			return r.updateOverlay(c, srt, origin, o.ID, &o, "plugin:"+origin)
		},
		OverlayRemove: func(c context.Context, id string) error {
			return r.updateOverlay(c, srt, origin, id, nil, "plugin:"+origin)
		},
		OverlayList: func(c context.Context) ([]reconcile.Overlay, error) { return r.Overlays(c, in.Server, origin) },
		RequestSync: func(c context.Context, reason string) error {
			if _, err := r.opts.Store.Audit().Append(c, state.AuditEntry{Time: time.Now(), Server: in.Server, Actor: "plugin:" + origin, Action: "server.sync.request", Target: in.Server, Details: mustJSONReason(reason)}); err != nil {
				return err
			}
			if !r.syncMu.TryLock() {
				return errors.New("sync already in progress")
			}
			defer r.syncMu.Unlock()
			a := r.Active()
			if a == nil || a.servers[in.Server] != srt {
				return errors.New("installation is activating")
			}
			_, _, _, err := r.reconcileServer(c, a.rev, srt, triggerManual)
			return err
		},
		Incident: func(c context.Context, actions discord.IncidentActions) error {
			r.pauseMu.RLock()
			defer r.pauseMu.RUnlock()
			if _, paused := r.pauses.m[in.Server]; paused {
				return reconcile.ErrPaused
			}
			cl, ok := host.session.(discord.IncidentClient)
			if !ok {
				return errors.New("incident actions unavailable")
			}
			if err := cl.SetIncident(c, srt.cfg.Spec.DiscordID, actions); err != nil {
				return err
			}
			data, _ := json.Marshal(actions)
			_, err := r.opts.Store.Audit().Append(c, state.AuditEntry{Time: time.Now(), Server: in.Server, Actor: "plugin:" + origin, Action: "server.incident", Target: in.Server, Details: data})
			return err
		},
		UploadLimit: func() int { return discord.UploadLimit(srt.cache.Server().PremiumTier) },
		Pseudonym:   func(user string) string { return r.Pseudonym(in.Server, user) },
		BeforeStart: func(ctx context.Context, inst *plugin.Instance) error {
			return r.restoreMemberData(ctx, in.Server, instanceScope(in), inst)
		},
		AnalyticsSummary: r.pluginSummary(in.Server, srt),
		BudgetedSecrets:  budgeted,
		ReserveBudget: func(ctx context.Context, name string, units int) (func(context.Context, int) error, error) {
			return r.reserveProvider(ctx, in, name, units)
		},
		Protection: func(ctx context.Context) plugin.ServerProtection { return r.protection(ctx, host.session, srt.cfg) },
		ManagedResource: func(id string) (bool, bool) {
			managed := srt.cache.Owner(id) == state.OwnerGit
			return managed, managed && srt.cfg.Spec.SyncPolicy.SelfHeal() && !r.isPaused(in.Server)
		},
		Operations: r.operationScope(discord.Operation{ServerID: srt.cfg.Spec.DiscordID, ActorID: host.session.Self().ID, Bot: in.Bot, Instance: in.Name, Plugin: m.Metadata.Name}),
		AttributeAudit: func(ctx context.Context, e discord.AuditLogEntryEvent) discord.AuditLogEntryEvent {
			return r.enrichAudit(ctx, host.session, e)
		},
		DeclaredMemberRoles: declaredMemberRoles, DeclaredRolesByUser: declaredRolesByUser,
		BotKey: in.Bot, InstanceName: in.Name, PluginName: m.Metadata.Name, PluginVersion: m.Metadata.Version,
		Bot: host.session.Self(), ApplicationID: host.session.ApplicationID(),
		ServerKey: in.Server, ServerID: srt.cfg.Spec.DiscordID, ServerName: srvName, ServerIconURL: srt.cache.Server().IconURL, ServerPreferredLocale: srt.cache.Server().PreferredLocale,
		Index:  r.opts.Store.Index(instanceScope(in)),
		Config: in.Config, Grants: in.Grants, NetworkAllow: in.NetworkAllow, Files: in.Files, Secret: func(n string) (string, []string, bool) { return r.instanceSecret(in, n) }, AllowPrivateNetworks: r.opts.AllowPrivateNetworks,
		ReportStatus:   irt.featureStatus,
		MessageContent: slices.Contains(in.EffectiveIntents(), "MessageContent"), HTTPClient: r.opts.PluginHTTPClient,
		ServerModeration:  slices.Contains(in.EffectiveIntents(), "ServerModeration"),
		ServerMembers:     slices.Contains(in.EffectiveIntents(), "ServerMembers"),
		ServerVoiceStates: slices.Contains(in.EffectiveIntents(), "ServerVoiceStates"),
		Discord:           host.session, Resolver: srt.cache,
		VoiceStates: func() ([]discord.VoiceState, bool) {
			return host.voice.States(srt.cfg.Spec.DiscordID)
		},
		Members: func(ctx context.Context) ([]discord.Member, error) {
			return srt.cache.members.List(ctx, host.session, srt.cfg.Spec.DiscordID)
		},
		AFKChannelID: func() string { return srt.cache.Server().AFKChannelID },
		OwnerID:      func() string { return discord.CurrentGuildOwner(host.session, srt.cfg.Spec.DiscordID) }, Access: pluginAccess(srt.cfg.Spec.Access),
		DMReceipts: r.opts.Store.KV("dm-receipts:" + in.ID()), KV: r.opts.Store.KV(instanceScope(in)), Tasks: r.opts.Store.Tasks(), TaskScope: instanceScope(in),
		Resources:          ownedResources{ResourceMap: r.opts.Store.Resources(), cache: srt.cache},
		Portal:             &auth.Linker{Signer: r.opts.Signer, BaseURL: r.opts.PortalURL, Server: in.Server, Store: r.opts.Store, Bot: in.Bot, Instance: in.Name, Plugin: m.Metadata.Name, MaxTTL: 24 * time.Hour},
		IsolateMemberCalls: m.Spec.Interop != nil && len(m.Spec.Interop.Consumes.Actions)+len(m.Spec.Interop.Consumes.Queries) > 0,
		Features:           in.Features,
		ReportFeature: func(name string, active bool, reason string) error {
			if _, ok := in.Features[name]; !ok {
				return fmt.Errorf("undeclared optional feature %q", name)
			}
			if active {
				reason = ""
			} else if reason == "" {
				reason = "disabled by instance configuration"
			}
			irt.featureReports.Store(name, reason)
			return nil
		},
		HasFeature: func(name, feature string) bool {
			for _, peer := range irt.revision.instances {
				if peer.cfg.Server != in.Server || peer.cfg.Plugin.Manifest.Metadata.Name != name || peer.inst() == nil {
					continue
				}
				if v, ok := peer.featureState()[feature]; ok && v == "" {
					return true
				}
			}
			return false
		},
		HasPlugin: func(name string) bool {
			_, err := resolvePeer(irt, name)
			var e *plugin.InteropError
			return !errors.As(err, &e) || !e.Unavailable
		},
		InvokePlugin: func(ctx context.Context, kind string, a plugin.Request, f plugin.CallFrame) (json.RawMessage, error) {
			return r.invokePeer(ctx, irt, kind, a, f)
		},
		PublishVersion: func(ctx context.Context, name string, v int, key string, data json.RawMessage) error {
			return r.publishEvent(ctx, irt, name, v, key, data)
		},
		Logger:    r.log, // the instance adds bot, plugin, instance and server
		Limits:    r.opts.PluginLimits,
		OnHandler: r.onHandler(in),
	}
	inst, err := plugin.NewInstance(irt.plugin.code, irt.plugin.def, env)
	if err != nil {
		return nil, err
	}
	if err := inst.Start(ctx); err != nil {
		_ = inst.Stop(ctx)
		return nil, err
	}
	return inst, nil
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
	o.cache.Remember(res.Kind, res.Key, res.ID, res.Owner)
	return nil
}

// install builds the routing table for this bot from the new revision, syncs its slash
// commands per server and applies profile changes.
func (h *botHost) install(ctx context.Context, a *active) {
	rt := &routing{
		byServer: map[string][]*instanceRT{}, byName: map[string]map[string]*instanceRT{},
		commands: map[string]map[string]*instanceRT{}, reconcilers: map[string]*serverRT{}, caches: map[string]*serverCache{},
		serverKeys: map[string]string{},
	}
	b := a.repo.Bots[h.key]
	if b == nil {
		h.routing.Store(rt)
		return
	}
	if b.Spec.DirectMessages != nil {
		rt.dmOwner = b.Spec.DirectMessages.Instance
	}
	rt.profileRoot, rt.profileFile = a.repo.Root, b.File
	rt.guildProfiles = map[string]config.BotServer{}
	for _, binding := range b.Spec.Servers {
		if srv := a.repo.Servers[binding.Key]; srv != nil && binding.Profile != nil {
			rt.guildProfiles[srv.Spec.DiscordID] = binding
		}
	}
	for _, sk := range b.Spec.ServerKeys() {
		if s := a.servers[sk]; s != nil {
			if rt.dmLocale == "" && s.cfg.Spec.Portal != nil {
				rt.dmLocale = s.cfg.Spec.Portal.Locale
			}
			rt.caches[s.cfg.Spec.DiscordID] = s.cache
			if s.bot == h.key {
				rt.reconcilers[s.cfg.Spec.DiscordID] = s
			}
		}
	}
	for _, i := range a.instances {
		if i.cfg.Bot != h.key || i.inst() == nil {
			continue
		}
		sid := i.serverID
		rt.byServer[sid] = append(rt.byServer[sid], i)
		if rt.byName[i.cfg.Name] == nil {
			rt.byName[i.cfg.Name] = map[string]*instanceRT{}
		}
		rt.byName[i.cfg.Name][sid] = i
		for _, c := range i.plugin.def.DiscordCommands() {
			if rt.commands[sid] == nil {
				rt.commands[sid] = map[string]*instanceRT{}
			}
			rt.commands[sid][c.Type.Key(c.Name)] = i
		}
	}
	for _, sk := range b.Spec.ServerKeys() {
		if s := a.servers[sk]; s != nil {
			rt.serverKeys[s.cfg.Spec.DiscordID] = sk
		}
	}
	if len(rt.byName[rt.dmOwner]) == 0 {
		rt.dmOwner = ""
	}
	h.routing.Store(rt)

	// slash commands per server
	for sid := range rt.serverKeys {
		h.syncCommands(ctx, rt, sid)
	}

	// Per-server appearance is applied to this bot's member identity only.
	for sid := range rt.serverKeys {
		h.applyGuildProfile(ctx, rt, sid, false)
	}

	// profile
	if p := b.Spec.Profile; p != nil {
		var sp discord.SelfParams
		sig := p.Username
		if p.Username != "" && p.Username != h.session.Self().Username {
			u := p.Username
			sp.Username = &u
		}
		// Images are files relative to bot.yaml; the signature carries their hashes, so an
		// unchanged profile is not uploaded again.
		images := []struct {
			name, path string
			dst        **[]byte
		}{{"avatar", p.Avatar, &sp.Avatar}, {"banner", p.Banner, &sp.Banner}}
		for _, img := range images {
			if img.path == "" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(a.repo.Root, filepath.Dir(b.File), filepath.FromSlash(img.path)))
			if err != nil {
				h.rt.log.Warn("bot profile: read "+img.name, "bot", h.key, "err", err)
				continue
			}
			sum := sha256.Sum256(data)
			sig += "|" + img.name + ":" + hex.EncodeToString(sum[:])
			*img.dst = &data
		}
		if h.profileHash == sig {
			sp.Avatar, sp.Banner = nil, nil
		}
		if (sp.Username != nil || sp.Avatar != nil || sp.Banner != nil) && h.profileHash != sig {
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
	if m, ok := ev.(discord.MessageCreateEvent); ok && m.Message.ServerID == "" {
		if !m.Message.Author.Bot && h.routing.Load().dmOwner != "" {
			h.queueDM(func() { h.routeDM(m.Message, time.Now()) })
		}
		return
	}
	if e, ok := ev.(discord.InteractionCreateEvent); ok && e.Interaction.ServerID == "" {
		if strings.HasPrefix(e.Interaction.CustomID, dmSelect) {
			h.queueDM(func() { h.selectDM(e.Interaction, time.Now()) })
			return
		}
		if strings.HasPrefix(e.Interaction.CustomID, "dm/") {
			h.queueDM(func() { h.routeDMComponent(e.Interaction) })
			return
		}
	}
	if _, ok := ev.(discord.GatewayResumedEvent); ok {
		rt := h.routing.Load()
		for sid := range rt.byServer {
			h.afterAvailability(rt, sid)
		}
		return
	}
	h.rt.diag.observe(ev)
	if c := h.rt.collector; c != nil {
		c.Handle(h.key, ev) // before the early returns below (interactions, own messages)
	}
	ev = h.voice.Observe(ev) // fills VoiceStateUpdateEvent.Previous
	rt := h.routing.Load()
	sid := discord.ServerIDOf(ev)
	if c := rt.caches[sid]; c != nil {
		ev = c.previousEvent(ev)
		c.Observe(ev)
	}
	if s := rt.reconcilers[sid]; s != nil && s.rec != nil && s.rec.HandleEvent(ev) {
		// Drift: the server's sync state, plan, resources and drift changed.
		h.rt.notify(ChangeSync, s.key)
	}
	switch e := ev.(type) {
	case discord.ServerAvailableEvent:
		h.afterAvailability(rt, e.ServerID)
		return
	case discord.InteractionCreateEvent:
		h.routeInteraction(rt, e.Interaction)
		return
	case discord.ServerJoinEvent, discord.ServerLeaveEvent:
		h.serverMembership(rt, ev)
	case discord.MessageCreateEvent:
		if e.Message.Author.ID == h.session.Self().ID {
			return // never feed a bot its own messages (loops)
		}
	case discord.MessageUpdateEvent:
		if e.Message.Author.ID == h.session.Self().ID {
			return
		}
	}
	if entry, ok := ev.(discord.AuditLogEntryEvent); ok {
		h.dispatchAudit(entry)
		return
	}
	h.dispatchPlugins(rt, ev)
}

// A full reconnect can miss changes made while offline. Subscribers receive this
// notification only after authoritative channel and role data has replaced the old cache.
func (h *botHost) afterAvailability(rt *routing, sid string) {
	cache := rt.caches[sid]
	if cache == nil {
		return
	}
	for _, i := range rt.byServer[sid] {
		if !i.plugin.def.HasEvent("serverAvailable") {
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := cache.Refresh(ctx, h.session, h.rt.Store().Resources()); err != nil {
				h.rt.log.Warn("refresh permissions after gateway reconnect", "server", sid, "err", err)
				return
			}
			if rt == h.routing.Load() {
				h.dispatchPlugins(rt, discord.ServerAvailableEvent{ServerID: sid})
			}
		}()
		break
	}
}

func (h *botHost) dispatchPlugins(rt *routing, ev discord.Event) {
	sid := discord.ServerIDOf(ev)
	name := plugin.EventName(ev)
	if name == "" {
		return
	}
	for _, i := range rt.byServer[sid] {
		if !i.plugin.def.HasEvent(name) || !hasEventIntents(i.cfg, name) {
			continue
		}
		done := i.inst().DispatchEvent(context.Background(), ev)
		go func() {
			err := <-done
			h.rt.handlerDone(i, err)
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
		target, kind, localID = rt.commands[it.ServerID][it.CommandType.Key(it.CommandName)], "command", it.CommandName
		if it.CommandType == discord.UserMenu || it.CommandType == discord.MessageMenu {
			kind = "contextMenu"
		}
	case discord.InteractionAutocomplete:
		target, kind, localID = rt.commands[it.ServerID][it.CommandName], "autocomplete", it.CommandName
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
		resp := discord.InteractionResponse{
			Type: discord.ResponseMessage,
			Data: &discord.MessageCreate{Content: "This action is no longer available.", Flags: discord.MessageFlagEphemeral},
		}
		if kind == "autocomplete" {
			resp = discord.InteractionResponse{Type: discord.ResponseAutocomplete, Choices: []discord.CommandChoice{}}
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = h.session.RespondInteraction(ctx, it.ID, it.Token, resp)
		}()
		return
	}
	if it.ServerID == "" { // Historical bot messages with an unqualified component ID.
		h.queueDM(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			m, err := h.session.GetMember(ctx, target.serverID, it.User.ID)
			if err == nil && m.User.ID == it.User.ID {
				it.Member = &m
				h.dispatchInteraction(target, it, kind, localID)
			}
		})
		return
	}
	h.dispatchInteraction(target, it, kind, localID)
}

func (h *botHost) dispatchInteraction(target *instanceRT, it discord.Interaction, kind, localID string) {
	inst := target.inst()
	if inst == nil {
		return
	}
	done := inst.DispatchInteraction(context.Background(), it, kind, localID)
	go func() {
		err := <-done
		h.rt.handlerDone(target, err)
		if err != nil {
			h.rt.log.Warn("plugin interaction failed", "instance", target.id(), "kind", kind, "id", localID, "err", err)
		}
	}()
}

// A gateway session unions every installation's intents. Deliver an optional handler
// only when the receiving installation itself has the required intents.
func hasEventIntents(in config.Instance, name string) bool {
	for _, n := range plugin.EventIntents(name) {
		if !slices.Contains(in.EffectiveIntents(), n) {
			return false
		}
	}
	return true
}

// Serialize activations with a bot rejoining its guild. Rejoining creates a new member identity.
func (h *botHost) applyGuildProfile(ctx context.Context, rt *routing, sid string, joined bool) {
	h.guildProfileMu.Lock()
	defer h.guildProfileMu.Unlock()
	if rt != h.routing.Load() {
		return
	}
	binding, ok := rt.guildProfiles[sid]
	if !ok || joined {
		delete(h.guildProfileHashes, sid)
	}
	if !ok {
		return
	}
	if err := h.syncGuildProfile(ctx, rt.profileRoot, rt.profileFile, binding, sid); err != nil {
		h.rt.log.Warn("update guild bot profile", "bot", h.key, "server", binding.Key, "err", err)
	}
}

func (h *botHost) syncGuildProfile(ctx context.Context, root, file string, b config.BotServer, serverID string) error {
	p := discord.GuildProfile{Nickname: b.Profile.Nickname, Bio: b.Profile.Bio}
	for _, image := range []struct {
		path *string
		dst  **[]byte
	}{{b.Profile.Avatar, &p.Avatar}, {b.Profile.Banner, &p.Banner}} {
		if image.path == nil {
			continue
		}
		data := []byte{}
		if *image.path != "" {
			rel, ok := config.ResolveRepoPath(file, *image.path)
			if !ok {
				return fmt.Errorf("profile image outside repository")
			}
			r, err := os.OpenRoot(root)
			if err != nil {
				return err
			}
			data, err = r.ReadFile(filepath.FromSlash(rel))
			_ = r.Close()
			if err != nil {
				return err
			}
		}
		*image.dst = &data
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return err
	}
	sig := sha256.Sum256(encoded)
	if h.guildProfileHashes == nil {
		h.guildProfileHashes = map[string][32]byte{}
	}
	if h.guildProfileHashes[serverID] == sig {
		return nil
	}
	if err = h.session.UpdateGuildProfile(ctx, serverID, p); err != nil {
		return err
	}
	h.guildProfileHashes[serverID] = sig
	return nil
}
