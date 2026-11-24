package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/npjonath/cicord/internal/catalog"
	"github.com/npjonath/cicord/internal/config"
	"github.com/npjonath/cicord/internal/discord"
	"github.com/npjonath/cicord/internal/reconcile"
	"github.com/npjonath/cicord/internal/registry"
	"github.com/npjonath/cicord/internal/runtime"
	"github.com/npjonath/cicord/internal/state"
	"github.com/npjonath/cicord/internal/store"
)

// visibleServers returns the servers where the caller has at least min.
func (s *server) visibleServers(ctx context.Context, id *identity, min role) (map[string]role, error) {
	out := map[string]role{}
	for _, key := range s.rt.ServerKeys() {
		r, _, err := s.roleIn(ctx, id, key, false)
		if err != nil {
			return nil, err
		}
		if r >= min {
			out[key] = r
		}
	}
	return out, nil
}

// ───────────────────────────────────────────────────────────── §3 status

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAuth(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.rt == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "runtime not available"))
		return
	}
	st := s.rt.Status()
	if st.Revision == nil {
		// Before the first revision no server roles can be resolved: only bearer clients
		// get the details (lastError may contain configuration content).
		if !s.isBearer(r) {
			st.LastError = ""
			st.Bots = []runtime.BotStatus{}
		}
		writeJSON(w, http.StatusOK, humanStatus(st))
		return
	}
	vis, err := s.visibleServers(r.Context(), id, roleModerator)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(vis) == 0 {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "you are not an admin or moderator of any server"))
		return
	}
	out := humanStatus(s.filterStatus(r.Context(), st, vis))
	for i := range out.Servers {
		s.addLive(&out.Servers[i])
	}
	writeJSON(w, http.StatusOK, out)
}

// ───────────────────────────────────────────────────────────── error objects
//
// The runtime keeps errors as text (status messages, sync history). The DTOs below add
// the humanized form (spec/api.md "Error object") next to the unchanged raw fields.

type serverStatusDTO struct {
	runtime.ServerStatus
	Error *reconcile.HumanError `json:"error,omitempty"`
	liveServerDTO
}

// liveServerDTO is the live Discord state of a server (from the runtime cache, kept
// current by GUILD_UPDATE events): images, boost level and feature flags.
type liveServerDTO struct {
	IconURL   string `json:"iconUrl"`
	BannerURL string `json:"bannerUrl"`
	SplashURL string `json:"splashUrl"`
	// BoostLevel is Discord's premium tier (0-3), BoostCount the number of boosts.
	BoostLevel int      `json:"boostLevel"`
	BoostCount int      `json:"boostCount"`
	Features   []string `json:"features"`
	// Community is true when the COMMUNITY feature is enabled.
	Community bool `json:"community"`
}

func liveOf(info discord.Server) liveServerDTO {
	f := append([]string{}, info.Features...)
	sort.Strings(f)
	return liveServerDTO{
		IconURL: info.IconURL, BannerURL: info.BannerURL, SplashURL: info.SplashURL,
		BoostLevel: info.PremiumTier, BoostCount: info.PremiumSubscriptionCount,
		Features: f, Community: info.HasFeature(discord.FeatureCommunity),
	}
}

// addLive fills the live state of a server DTO (features is [] when the server is not
// cached yet).
func (s *server) addLive(d *serverStatusDTO) {
	info, _ := s.rt.ServerInfo(d.Key)
	d.liveServerDTO = liveOf(info)
}

type botStatusDTO struct {
	runtime.BotStatus
	Error *reconcile.HumanError `json:"error,omitempty"`
}

type statusDTO struct {
	runtime.Status
	// LastErrorInfo is LastError explained.
	LastErrorInfo *reconcile.HumanError `json:"lastErrorInfo,omitempty"`
	Servers       []serverStatusDTO     `json:"servers"`
	Bots          []botStatusDTO        `json:"bots"`
}

// statusError explains a status message when the health reports a problem (Healthy
// servers may carry informational messages such as "2 change(s) blocked").
func statusError(health, message string) *reconcile.HumanError {
	if message == "" || (health != runtime.HealthDegraded && health != runtime.HealthMissing) {
		return nil
	}
	return reconcile.Humanize(message)
}

func humanServer(ss runtime.ServerStatus) serverStatusDTO {
	return serverStatusDTO{ServerStatus: ss, Error: statusError(ss.Health, ss.Message), liveServerDTO: liveServerDTO{Features: []string{}}}
}

func humanStatus(st runtime.Status) statusDTO {
	out := statusDTO{Status: st, LastErrorInfo: reconcile.Humanize(st.LastError),
		Servers: make([]serverStatusDTO, 0, len(st.Servers)), Bots: make([]botStatusDTO, 0, len(st.Bots))}
	for _, ss := range st.Servers {
		out.Servers = append(out.Servers, humanServer(ss))
	}
	for _, b := range st.Bots {
		out.Bots = append(out.Bots, botStatusDTO{BotStatus: b, Error: statusError(b.Health, b.Message)})
	}
	return out
}

type blockedDTO struct {
	reconcile.Blocked
	Error *reconcile.HumanError `json:"error,omitempty"`
}

type planDTO struct {
	Server  string             `json:"server"`
	Changes []reconcile.Change `json:"changes"`
	Blocked []blockedDTO       `json:"blocked"`
}

func humanPlan(p reconcile.Plan) planDTO {
	p = emptyPlan(p)
	out := planDTO{Server: p.Server, Changes: p.Changes, Blocked: make([]blockedDTO, 0, len(p.Blocked))}
	for _, b := range p.Blocked {
		out.Blocked = append(out.Blocked, blockedDTO{Blocked: b, Error: reconcile.ExplainBlocked(b)})
	}
	return out
}

// humanAPIError is the API error envelope (api.go apiError) with the humanized fields.
type humanAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Details string `json:"details,omitempty"`
	// Reason is the humanized error code (reconcile.HumanError.Code).
	Reason string `json:"reason,omitempty"`
}

// failHuman answers a Discord / reconcile failure with an explained error.
func failHuman(w http.ResponseWriter, status int, code, prefix string, err error) {
	h := reconcile.HumanizeErr(err)
	writeJSON(w, status, map[string]any{"error": humanAPIError{
		Code: code, Message: h.Message, Hint: h.Hint, Details: prefix + err.Error(), Reason: h.Code,
	}})
}

func (s *server) filterStatus(ctx context.Context, st runtime.Status, vis map[string]role) runtime.Status {
	servers := []runtime.ServerStatus{}
	for _, ss := range st.Servers {
		if _, ok := vis[ss.Key]; !ok {
			continue
		}
		if d, err := s.rt.Drift(ctx, ss.Key); err == nil && d != nil {
			ss.Drift = d
		}
		servers = append(servers, ss)
	}
	st.Servers = servers
	bots := []runtime.BotStatus{}
	for _, b := range st.Bots {
		seen := false
		for _, sk := range b.Servers {
			if _, ok := vis[sk]; ok {
				seen = true
			}
		}
		if !seen {
			continue
		}
		plugins := []runtime.PluginStatus{}
		for _, p := range b.Plugins {
			if _, ok := vis[p.Server]; ok {
				plugins = append(plugins, p)
			}
		}
		b.Plugins = plugins
		bots = append(bots, b)
	}
	st.Bots = bots
	return st
}

func (s *server) serverStatus(ctx context.Context, key string) (runtime.ServerStatus, bool) {
	for _, ss := range s.rt.Status().Servers {
		if ss.Key == key {
			if d, err := s.rt.Drift(ctx, key); err == nil && d != nil {
				ss.Drift = d
			}
			return ss, true
		}
	}
	return runtime.ServerStatus{}, false
}

type syncRecordDTO struct {
	ID       int64           `json:"id"`
	Time     time.Time       `json:"time"`
	Revision string          `json:"revision"`
	Target   string          `json:"target"`
	Status   string          `json:"status"`
	Message  string          `json:"message"`
	Details  json.RawMessage `json:"details,omitempty"`
	// Error explains Message for failed and rejected records.
	Error *reconcile.HumanError `json:"error,omitempty"`
}

// queryLimit reads the "limit" query parameter: 50 by default, at most 500.
func queryLimit(r *http.Request) int {
	const defLimit, maxLimit = 50, 500
	v, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || v <= 0 {
		return defLimit
	}
	if v > maxLimit {
		return maxLimit
	}
	return v
}

func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	id, err := s.requireAuth(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.requireRuntime(); err != nil {
		s.fail(w, err)
		return
	}
	vis, err := s.visibleServers(r.Context(), id, roleModerator)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(vis) == 0 {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "you are not an admin or moderator of any server"))
		return
	}
	limit := queryLimit(r)
	// Over-fetch: visibility filtering happens after the query.
	recs, err := s.store.Syncs().List(r.Context(), r.URL.Query().Get("target"), min(limit*5, 1000))
	if err != nil {
		s.fail(w, err)
		return
	}
	// Project-level records (revisions) describe every server: only callers who can see
	// all servers get them. Bot records need one visible server of that bot.
	seesAll := true
	repo := s.rt.Repo()
	if repo != nil {
		for k := range repo.Servers {
			if _, ok := vis[k]; !ok {
				seesAll = false
			}
		}
	}
	items := []syncRecordDTO{}
	for _, rec := range recs {
		if sk, ok := cutPrefix(rec.Target, "server:"); ok {
			if _, visible := vis[sk]; !visible {
				continue
			}
		} else if bk, ok := cutPrefix(rec.Target, "bot:"); ok {
			visible := false
			if repo != nil && repo.Bots[bk] != nil {
				for _, sk := range repo.Bots[bk].Spec.Servers {
					if _, v := vis[sk]; v {
						visible = true
					}
				}
			}
			if !visible {
				continue
			}
		} else if !seesAll {
			continue
		}
		dto := syncRecordDTO{ID: rec.ID, Time: rec.Time, Revision: rec.Revision, Target: rec.Target,
			Status: rec.Status, Message: rec.Message, Details: rec.Details}
		if rec.Status != "succeeded" {
			dto.Error = reconcile.Humanize(rec.Message)
		}
		items = append(items, dto)
	}
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func cutPrefix(s, p string) (string, bool) {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return s, false
}

// ───────────────────────────────────────────────────────────── §4 servers

func (s *server) handleServer(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleModerator); err != nil {
		s.fail(w, err)
		return
	}
	ss, ok := s.serverStatus(r.Context(), key)
	srv, okCfg := s.rt.ServerConfig(key)
	if !ok || !okCfg {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown server %q", key))
		return
	}
	if ss.Drift == nil {
		ss.Drift = []runtime.DriftEntry{}
	}
	dto := humanServer(ss)
	s.addLive(&dto)
	writeJSON(w, http.StatusOK, struct {
		serverStatusDTO
		Spec any `json:"spec"`
	}{dto, srv.Spec})
}

type roleDTO struct {
	Key     string `json:"key"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	Managed bool   `json:"managed"`
}

type channelDTO struct {
	Key      string `json:"key"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Category string `json:"category,omitempty"`
	Managed  bool   `json:"managed"`
}

func colorHex(c int) string {
	if c == 0 {
		return ""
	}
	return fmt.Sprintf("#%06x", c)
}

func (s *server) handleResources(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleModerator); err != nil {
		s.fail(w, err)
		return
	}
	srv, _ := s.rt.ServerConfig(key)
	roles, channels, keyOf, _ := s.rt.ServerResources(key)
	declared := func(kind state.ResourceKind, id string) string {
		k, rk, ok := keyOf(id)
		if !ok || k != kind {
			return ""
		}
		switch kind {
		case state.KindRole:
			if _, ok := srv.Spec.Roles[rk]; ok {
				return rk
			}
		case state.KindCategory:
			if _, ok := srv.Spec.Categories[rk]; ok {
				return rk
			}
		case state.KindChannel:
			if _, ok := srv.Spec.Channels[rk]; ok {
				return rk
			}
		}
		return ""
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Position > roles[j].Position })
	sort.Slice(channels, func(i, j int) bool {
		if channels[i].Position != channels[j].Position {
			return channels[i].Position < channels[j].Position
		}
		return channels[i].ID < channels[j].ID
	})
	out := struct {
		Roles      []roleDTO    `json:"roles"`
		Categories []channelDTO `json:"categories"`
		Channels   []channelDTO `json:"channels"`
	}{[]roleDTO{}, []channelDTO{}, []channelDTO{}}
	for _, ro := range roles {
		if ro.IsEveryone(srv.Spec.DiscordID) {
			continue
		}
		k := declared(state.KindRole, ro.ID)
		out.Roles = append(out.Roles, roleDTO{Key: k, ID: ro.ID, Name: ro.Name, Color: colorHex(ro.Color), Managed: k != ""})
	}
	for _, ch := range channels {
		if ch.Type == discord.ChannelCategory {
			k := declared(state.KindCategory, ch.ID)
			out.Categories = append(out.Categories, channelDTO{Key: k, ID: ch.ID, Name: ch.Name, Managed: k != ""})
		}
	}
	for _, ch := range channels {
		if ch.Type == discord.ChannelCategory {
			continue
		}
		k := declared(state.KindChannel, ch.ID)
		cat := ""
		if ch.ParentID != "" {
			cat = declared(state.KindCategory, ch.ParentID)
		}
		out.Channels = append(out.Channels, channelDTO{Key: k, ID: ch.ID, Name: ch.Name, Type: ch.Type.String(), Category: cat, Managed: k != ""})
	}
	writeJSON(w, http.StatusOK, out)
}

func emptyPlan(p reconcile.Plan) reconcile.Plan {
	if p.Changes == nil {
		p.Changes = []reconcile.Change{}
	}
	if p.Blocked == nil {
		p.Blocked = []reconcile.Blocked{}
	}
	return p
}

func (s *server) handlePlan(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleAdmin); err != nil {
		s.fail(w, err)
		return
	}
	p, err := s.rt.PlanServer(r.Context(), key)
	if err != nil {
		failHuman(w, http.StatusServiceUnavailable, "unavailable", "plan: ", err)
		return
	}
	writeJSON(w, http.StatusOK, humanPlan(p))
}

func (s *server) handleServerSync(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	id, _, err := s.requireRole(r, key, roleAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	p, n, err := s.rt.SyncServer(r.Context(), key)
	if err != nil {
		failHuman(w, http.StatusServiceUnavailable, "unavailable", "sync: ", err)
		return
	}
	s.audit(r.Context(), state.AuditEntry{Server: key, Actor: id.auditActor(), Action: "sync.apply", Target: key,
		Details: mustJSON(map[string]any{"changes": n})})
	writeJSON(w, http.StatusOK, map[string]any{"plan": humanPlan(p), "applied": n})
}

func (id *identity) auditActor() string {
	if id.bearer {
		return "runtime-token"
	}
	if id.setup {
		return "setup"
	}
	return "discord:" + id.user.ID
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func (s *server) audit(ctx context.Context, e state.AuditEntry) {
	if _, err := s.store.Audit().Append(ctx, e); err != nil {
		s.log.Warn("audit append", "err", err)
	}
}

type auditDTO struct {
	ID      int64           `json:"id"`
	Time    time.Time       `json:"time"`
	Server  string          `json:"server"`
	Actor   string          `json:"actor"`
	Action  string          `json:"action"`
	Target  string          `json:"target"`
	Details json.RawMessage `json:"details"`
}

func (s *server) handleAudit(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	if _, _, err := s.requireRole(r, key, roleModerator); err != nil {
		s.fail(w, err)
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	entries, err := s.store.Audit().List(r.Context(), state.AuditFilter{Server: key, Before: before, Limit: queryLimit(r)})
	if err != nil {
		s.fail(w, err)
		return
	}
	items := make([]auditDTO, 0, len(entries))
	for _, e := range entries {
		d := e.Details
		if len(d) == 0 {
			d = json.RawMessage("{}")
		}
		items = append(items, auditDTO{ID: e.ID, Time: e.Time, Server: e.Server, Actor: e.Actor, Action: e.Action, Target: e.Target, Details: d})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ───────────────────────────────────────────────────────────── §5 plugins

type pluginInfoDTO struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	DisplayName  string   `json:"displayName"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
	Intents      []string `json:"intents"`
}

type commandDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type pluginItemDTO struct {
	Bot      string         `json:"bot"`
	Instance string         `json:"instance"`
	Server   string         `json:"server"`
	Plugin   pluginInfoDTO  `json:"plugin"`
	Config   map[string]any `json:"config"`
	// Schema is the plugin's config.schema.json as written (property order preserved).
	Schema json.RawMessage `json:"schema"`
	// BaseConfig is spec.plugins[].config of bot.yaml and Override the server's
	// overrides.<server>.config (null when none), both without defaults.
	BaseConfig map[string]any `json:"baseConfig"`
	Override   map[string]any `json:"override"`
	Editable   []string       `json:"editable"`
	Commands   []commandDTO   `json:"commands"`
	Routes     []string       `json:"routes"`
	Health     string         `json:"health"`
	Message    string         `json:"message"`
	// Source is where the plugin comes from ("cicord", a registry, or "local") and Trust
	// its trust level ("official", "unofficial" or "local").
	Source string `json:"source"`
	Trust  string `json:"trust"`
	// Update is set when a newer version of the plugin is available (Store §11).
	Update *store.UpdateInfo `json:"update,omitempty"`
}

// editableFields lists the top-level config fields a role may edit (x-cicord-access).
func editableFields(schema map[string]any, r role) []string {
	out := []string{}
	props, _ := schema["properties"].(map[string]any)
	for k, raw := range props {
		sub, _ := raw.(map[string]any)
		access, _ := sub["x-cicord-access"].(string)
		if access == "" {
			access = "admin"
		}
		if r >= roleAdmin || (r >= roleModerator && access == "moderator") {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func strs(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func (s *server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	_, ro, err := s.requireRole(r, key, roleModerator)
	if err != nil {
		s.fail(w, err)
		return
	}
	pq, err := parsePage(r, []string{"name", "instance", "bot", "version", "health", "source"}, "name")
	if err != nil {
		s.fail(w, err)
		return
	}
	items := []pluginItemDTO{}
	ups := s.pluginStore().Updates(r.Context(), s.rt.Repo(), store.CacheOnly)
	onlyUpdates := r.URL.Query().Get("updates")
	for _, in := range s.rt.Instances(key) {
		it := s.pluginItem(in, key, ro, ups)
		if onlyUpdates != "" && (onlyUpdates == "true") != (it.Update != nil) {
			continue
		}
		items = append(items, it)
	}
	out, meta := page(items, pq,
		func(it pluginItemDTO) string {
			return strings.Join([]string{it.Plugin.Name, it.Plugin.DisplayName, it.Plugin.Description, it.Instance, it.Bot, it.Source}, " ")
		},
		func(it pluginItemDTO, k string) string {
			switch k {
			case "instance":
				return it.Instance
			case "bot":
				return it.Bot
			case "version":
				return it.Plugin.Version
			case "health":
				return it.Health
			case "source":
				return it.Source
			}
			if it.Plugin.DisplayName != "" {
				return it.Plugin.DisplayName
			}
			return it.Plugin.Name
		},
		func(it pluginItemDTO) string { return it.Bot + "/" + it.Instance })
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": meta.Total, "offset": meta.Offset,
		"limit": meta.Limit, "sort": meta.Sort, "order": meta.Order})
}

// handlePlugin is one instance (the admin's per-plugin page).
func (s *server) handlePlugin(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("server")
	_, ro, err := s.requireRole(r, key, roleModerator)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, in := range s.rt.Instances(key) {
		if in.Config.Bot == r.PathValue("bot") && in.Config.Name == r.PathValue("instance") {
			writeJSON(w, http.StatusOK, s.pluginItem(in, key, ro, s.pluginStore().Updates(r.Context(), s.rt.Repo(), store.CacheOnly)))
			return
		}
	}
	s.fail(w, httpErr(http.StatusNotFound, "not_found", "no plugin instance %s/%s on %s", r.PathValue("bot"), r.PathValue("instance"), key))
}

func (s *server) pluginItem(in runtime.InstanceInfo, key string, ro role, ups map[string]store.UpdateInfo) pluginItemDTO {
	m := in.Config.Plugin.Manifest
	schema := in.Config.Plugin.SchemaDoc
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	base, override := s.rawInstanceConfig(in.Config.Bot, in.Config.Name, key)
	it := pluginItemDTO{
		Bot: in.Config.Bot, Instance: in.Config.Name, Server: in.Config.Server,
		Plugin: pluginInfoDTO{Name: m.Metadata.Name, Version: m.Metadata.Version, DisplayName: m.Metadata.DisplayName,
			Description: m.Metadata.Description, Capabilities: strs(m.Spec.Capabilities), Intents: strs(m.Spec.Intents)},
		Config: in.Config.Config, Schema: rawSchema(in.Config.Plugin, schema), Editable: editableFields(schema, ro),
		BaseConfig: base, Override: override,
		Commands: []commandDTO{}, Routes: []string{}, Health: in.Health, Message: in.Message,
	}
	it.Source, it.Trust = s.pluginOrigin(in.Config.Plugin)
	if u, ok := ups[it.Source+"/"+m.Metadata.Name]; ok && it.Source != "local" {
		it.Update = &u
	}
	if it.Config == nil {
		it.Config = map[string]any{}
	}
	if in.Definition != nil {
		for _, c := range in.Definition.Commands {
			it.Commands = append(it.Commands, commandDTO{Name: c.Name, Description: c.Description})
		}
		for _, rt := range in.Definition.Routes {
			it.Routes = append(it.Routes, rt.Key())
		}
	}
	return it
}

// pluginOrigin is the Store source of a loaded plugin ("cicord", a registry name, or
// "local" for a plugin written in the repository) and its trust.
func (s *server) pluginOrigin(lp *config.LoadedPlugin) (source, trust string) {
	if lp == nil || lp.Manifest == nil {
		return "local", "local"
	}
	if !strings.HasPrefix(lp.Source, ".") {
		reg, _, _ := strings.Cut(lp.Source, "/")
		trust = registry.TrustUnofficial
		if _, ok := registry.Official(); ok && reg == config.OfficialRegistryName {
			trust = registry.TrustOfficial // the implicit official registry (cicord.yaml cannot declare "cicord")
		} else if repo := s.rt.Repo(); repo != nil && repo.Project != nil {
			for _, rg := range repo.Project.Spec.Registries {
				if rg.Name == reg {
					trust = registry.Trust(rg.URL)
				}
			}
		}
		return reg, trust
	}
	name := lp.Manifest.Metadata.Name
	if repo := s.rt.Repo(); repo != nil && s.opts.Catalog != nil {
		if _, ok := s.opts.Catalog.Get(name); ok &&
			filepath.Clean(lp.Dir) == filepath.Join(repo.Root, filepath.FromSlash(catalog.PluginDir(name))) {
			return config.OfficialRegistryName, registry.TrustOfficial
		}
	}
	return "local", "local"
}

// rawSchema returns the config schema file bytes (key order preserved), falling back to
// the decoded document.
func rawSchema(lp *config.LoadedPlugin, doc map[string]any) json.RawMessage {
	if lp != nil && lp.Manifest != nil && lp.Manifest.Spec.Config != nil {
		p := filepath.Join(lp.Dir, filepath.FromSlash(lp.Manifest.Spec.Config.Schema))
		if data, err := os.ReadFile(p); err == nil && json.Valid(data) {
			var buf bytes.Buffer
			if json.Compact(&buf, data) == nil {
				return buf.Bytes()
			}
		}
	}
	return mustJSON(doc)
}

// rawInstanceConfig returns the base config and the server override of an instance as
// written in bot.yaml (normalized to JSON values).
func (s *server) rawInstanceConfig(botKey, instance, serverKey string) (base, override map[string]any) {
	base = map[string]any{}
	repo := s.rt.Repo()
	if repo == nil || repo.Bots[botKey] == nil {
		return base, nil
	}
	for _, p := range repo.Bots[botKey].Spec.Plugins {
		if p.Name != instance {
			continue
		}
		if b, err := config.NormalizeConfig(p.Config); err == nil {
			base = b
		}
		if ov, ok := p.Overrides[serverKey]; ok && ov.Config != nil {
			if o, err := config.NormalizeConfig(ov.Config); err == nil {
				override = o
			}
		}
	}
	return base, override
}
