package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/reconcile"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/state"
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
		writeJSON(w, http.StatusOK, st) // nothing to filter before the first revision
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
	writeJSON(w, http.StatusOK, s.filterStatus(r.Context(), st, vis))
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
}

func queryInt(r *http.Request, name string, def, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || v <= 0 {
		return def
	}
	if v > max {
		return max
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
	limit := queryInt(r, "limit", 50, 500)
	recs, err := s.store.Syncs().List(r.Context(), r.URL.Query().Get("target"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	items := []syncRecordDTO{}
	for _, rec := range recs {
		if sk, ok := cutPrefix(rec.Target, "server:"); ok {
			if _, visible := vis[sk]; !visible {
				continue
			}
		}
		items = append(items, syncRecordDTO{ID: rec.ID, Time: rec.Time, Revision: rec.Revision, Target: rec.Target,
			Status: rec.Status, Message: rec.Message, Details: rec.Details})
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
	ss, _ := s.serverStatus(r.Context(), key)
	srv, _ := s.rt.ServerConfig(key)
	writeJSON(w, http.StatusOK, struct {
		runtime.ServerStatus
		Spec any `json:"spec"`
	}{ss, srv.Spec})
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
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "plan: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, emptyPlan(p))
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
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "sync: %v", err))
		return
	}
	s.audit(r.Context(), state.AuditEntry{Server: key, Actor: id.auditActor(), Action: "sync.apply", Target: key,
		Details: mustJSON(map[string]any{"changes": n})})
	writeJSON(w, http.StatusOK, map[string]any{"plan": emptyPlan(p), "applied": n})
}

func (id *identity) auditActor() string {
	if id.bearer {
		return "runtime-token"
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
	entries, err := s.store.Audit().List(r.Context(), state.AuditFilter{Server: key, Before: before, Limit: queryInt(r, "limit", 50, 500)})
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
	Schema   map[string]any `json:"schema"`
	Editable []string       `json:"editable"`
	Commands []commandDTO   `json:"commands"`
	Routes   []string       `json:"routes"`
	Health   string         `json:"health"`
	Message  string         `json:"message"`
}

// editableFields lists the top-level config fields a role may edit (x-codi-access).
func editableFields(schema map[string]any, r role) []string {
	out := []string{}
	props, _ := schema["properties"].(map[string]any)
	for k, raw := range props {
		sub, _ := raw.(map[string]any)
		access, _ := sub["x-codi-access"].(string)
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
	items := []pluginItemDTO{}
	for _, in := range s.rt.Instances(key) {
		m := in.Config.Plugin.Manifest
		schema := in.Config.Plugin.SchemaDoc
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		it := pluginItemDTO{
			Bot: in.Config.Bot, Instance: in.Config.Name, Server: in.Config.Server,
			Plugin: pluginInfoDTO{Name: m.Metadata.Name, Version: m.Metadata.Version, DisplayName: m.Metadata.DisplayName,
				Description: m.Metadata.Description, Capabilities: strs(m.Spec.Capabilities), Intents: strs(m.Spec.Intents)},
			Config: in.Config.Config, Schema: schema, Editable: editableFields(schema, ro),
			Commands: []commandDTO{}, Routes: []string{}, Health: in.Health, Message: in.Message,
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
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
