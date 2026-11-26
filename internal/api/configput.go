package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/writeback"
)

type configPutRequest struct {
	Config   map[string]any `json:"config"`
	Scope    string         `json:"scope"`
	Revision string         `json:"revision"`
}

type configPutResponse struct {
	Mode           string `json:"mode"`
	Revision       string `json:"revision"`
	PullRequestURL string `json:"pullRequestUrl"`
}

// changedKeys returns the top-level keys whose values differ between a and b.
func changedKeys(a, b map[string]any) []string {
	var out []string
	for k, av := range a {
		if bv, ok := b[k]; !ok || !writeback.JSONEqual(av, bv) {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// pruneNoopNulls removes null entries (deletions) of keys absent from base: they are
// no-ops in a merge patch and would only clutter the YAML.
func pruneNoopNulls(patch, base map[string]any) map[string]any {
	if patch == nil {
		return nil
	}
	for k, v := range patch {
		bv, inBase := base[k]
		switch tv := v.(type) {
		case nil:
			if !inBase {
				delete(patch, k)
			}
		case map[string]any:
			if bm, ok := bv.(map[string]any); ok {
				if sub := pruneNoopNulls(tv, bm); len(sub) == 0 {
					delete(patch, k)
				}
			}
		}
	}
	if len(patch) == 0 {
		return nil
	}
	return patch
}

// stripDefaults removes from cfg the values equal to their schema default that were not
// written in rawBase at the same place, so a config saved from the admin UI (which shows
// the effective config) does not copy every default into bot.yaml. It recurses into nested
// objects and the object elements of arrays (matched by index), like config.ApplyDefaults.
func stripDefaults(cfg, rawBase, schema map[string]any) map[string]any {
	stripDefaultsIn(schema, schema, cfg, rawBase, 0)
	return cfg
}

func stripDefaultsIn(root, schema, cfg, raw map[string]any, depth int) {
	if depth > 32 {
		return
	}
	props, _ := config.ResolveSchemaRef(root, schema)["properties"].(map[string]any)
	for k, v := range cfg {
		subRaw, _ := props[k].(map[string]any)
		if subRaw == nil {
			continue
		}
		sub := config.ResolveSchemaRef(root, subRaw)
		rv, written := raw[k]
		if d, ok := sub["default"]; ok && !written && writeback.JSONEqual(d, v) {
			delete(cfg, k)
			continue
		}
		switch tv := v.(type) {
		case map[string]any:
			rm, _ := rv.(map[string]any)
			stripDefaultsIn(root, sub, tv, rm, depth+1)
			if len(tv) == 0 && !written {
				delete(cfg, k) // an object only materialized by defaults
			}
		case []any:
			items, _ := sub["items"].(map[string]any)
			if items == nil {
				continue
			}
			ra, _ := rv.([]any)
			for i, e := range tv {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				var rm map[string]any
				if i < len(ra) {
					rm, _ = ra[i].(map[string]any)
				}
				stripDefaultsIn(root, items, em, rm, depth+1)
			}
		}
	}
}

func (s *server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	serverKey, botKey, instName := r.PathValue("server"), r.PathValue("bot"), r.PathValue("instance")
	id, urlRole, err := s.requireRole(r, serverKey, roleModerator)
	if err != nil {
		s.fail(w, err)
		return
	}
	var req configPutRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		s.fail(w, err)
		return
	}
	if req.Config == nil {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "config must be a JSON object"))
		return
	}
	switch req.Scope {
	case "":
		req.Scope = "server"
	case "server", "base":
	default:
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", `scope must be "server" or "base"`))
		return
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	rev, _ := s.rt.Revision()
	if req.Revision != "" && req.Revision != rev.ID {
		s.fail(w, httpErr(http.StatusConflict, "conflict",
			"the configuration changed since revision %s (active: %s); reload and retry", req.Revision, rev.ID))
		return
	}
	repo := s.rt.Repo()
	bot := repo.Bots[botKey]
	if bot == nil {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown bot %q", botKey))
		return
	}
	idx := slices.IndexFunc(bot.Spec.Plugins, func(p config.PluginInstance) bool { return p.Name == instName })
	// Instances of this plugin instance, per server.
	current := map[string]runtime.InstanceInfo{}
	for _, in := range s.rt.Instances("") {
		if in.Config.Bot == botKey && in.Config.Name == instName {
			current[in.Config.Server] = in
		}
	}
	cur, ok := current[serverKey]
	if idx < 0 || !ok {
		s.fail(w, httpErr(http.StatusNotFound, "not_found", "unknown plugin instance %s/%s on server %s", botKey, instName, serverKey))
		return
	}
	pi := bot.Spec.Plugins[idx]
	lp := cur.Config.Plugin
	schema := lp.SchemaDoc
	if schema == nil {
		schema = map[string]any{}
	}

	newCfg, err := config.NormalizeConfig(req.Config)
	if err != nil {
		s.fail(w, httpErr(http.StatusBadRequest, "invalid", "config: %v", err))
		return
	}
	rawBase, err := config.NormalizeConfig(pi.Config)
	if err != nil {
		s.fail(w, err)
		return
	}
	rawOverrides := map[string]map[string]any{}
	for sk, ov := range pi.Overrides {
		if ov.Config != nil {
			if rawOverrides[sk], err = config.NormalizeConfig(ov.Config); err != nil {
				s.fail(w, err)
				return
			}
		}
	}

	// New effective config per affected server, and the YAML edit.
	effective := map[string]map[string]any{}
	var edit writeback.EditFunc
	var cfgPath, what string
	if req.Scope == "server" {
		patch := writeback.DiffMergePatch(config.EffectiveConfig(lp, rawBase, nil), newCfg)
		patch = pruneNoopNulls(patch, rawBase)
		effective[serverKey] = config.EffectiveConfig(lp, rawBase, patch)
		edit = writeback.PluginConfigEdit(instName, serverKey, patch)
		cfgPath = fmt.Sprintf("/spec/plugins/%d/overrides/%s/config", idx, serverKey)
		what = "override for " + serverKey
	} else {
		base := stripDefaults(config.CloneConfig(newCfg), rawBase, schema)
		for sk := range current {
			effective[sk] = config.EffectiveConfig(lp, base, rawOverrides[sk])
		}
		edit = writeback.PluginConfigEdit(instName, "", base)
		cfgPath = fmt.Sprintf("/spec/plugins/%d/config", idx)
		what = "base config"
	}

	// Authorization: moderators may only change fields marked x-cicord-access: moderator.
	servers := make([]string, 0, len(effective))
	for sk := range effective {
		servers = append(servers, sk)
	}
	sort.Strings(servers)
	var changed []string
	for _, sk := range servers {
		ro := urlRole
		if sk != serverKey {
			if ro, _, err = s.roleIn(r.Context(), id, sk, false); err != nil {
				s.fail(w, err)
				return
			}
		}
		diff := changedKeys(current[sk].Config.Config, effective[sk])
		changed = append(changed, diff...)
		if len(diff) == 0 {
			continue
		}
		if ro < roleModerator {
			s.fail(w, httpErr(http.StatusForbidden, "forbidden", "the base config also applies to server %s, where you are not a moderator", sk))
			return
		}
		allowed := editableFields(schema, ro)
		var denied []string
		for _, k := range diff {
			if !slices.Contains(allowed, k) {
				denied = append(denied, k)
			}
		}
		if len(denied) > 0 {
			s.fail(w, httpErr(http.StatusForbidden, "forbidden", "you may not change: %s", strings.Join(denied, ", ")))
			return
		}
	}
	slices.Sort(changed)
	changed = slices.Compact(changed)

	// Validation (same rules as config.Load).
	var ds config.Diagnostics
	for _, sk := range servers {
		srv, ok := s.rt.ServerConfig(sk)
		if !ok {
			continue
		}
		ds = append(ds, config.ValidateInstanceConfig(lp, effective[sk], sk, &srv.Spec, bot.File, cfgPath, current[sk].Config.NetworkAllow)...)
	}
	if ds.HasErrors() {
		writeDiagnostics(w, "the configuration is invalid", ds.Errors())
		return
	}
	if slices.Contains(changed, "enabled") && slices.ContainsFunc(servers, func(sk string) bool { return current[sk].Config.Disabled() && !config.Disabled(effective[sk]) }) {
		in := setup.Installer{Runtime: s.rt}
		checks, err := in.ValidateActivation(rev.Dir, map[string]writeback.EditFunc{bot.File: edit})
		if err != nil {
			s.fail(w, err)
			return
		}
		if checks.HasErrors() {
			writeDiagnostics(w, "the installation cannot be enabled", checks.Errors())
			return
		}
	}
	if len(changed) == 0 {
		writeJSON(w, http.StatusOK, configPutResponse{Mode: writeback.ModeCommit, Revision: rev.ID})
		return
	}

	if s.opts.Writer == nil {
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "write-back is not configured"))
		return
	}
	wb := repo.Project.Spec.WriteBack
	if wb != nil && wb.Enabled != nil && !*wb.Enabled {
		s.fail(w, httpErr(http.StatusForbidden, "forbidden", "write-back is disabled in cicord.yaml"))
		return
	}
	mode := writeback.ModeFor(wb, []string{bot.File})
	prevErr := s.rt.Status().LastError
	res, err := s.opts.Writer.Write(r.Context(), writeback.Request{
		Edits:   map[string]writeback.EditFunc{bot.File: edit},
		Message: fmt.Sprintf("config(%s/%s): update %s\n\nChanged: %s", botKey, instName, what, strings.Join(changed, ", ")),
		Actor:   id.actor(),
		Source:  "admin-ui",
		Mode:    mode,
	})
	if err != nil {
		if errors.Is(err, writeback.ErrConflict) {
			s.fail(w, httpErr(http.StatusConflict, "conflict", "%v", err))
			return
		}
		s.log.Error("write-back failed", "err", err)
		s.fail(w, httpErr(http.StatusServiceUnavailable, "unavailable", "write-back failed: %v", err))
		return
	}
	if res.Mode == "" {
		res.Mode = mode
	}
	s.audit(r.Context(), state.AuditEntry{Server: serverKey, Actor: id.auditActor(), Action: "config.update",
		Target: botKey + "/" + instName, Details: mustJSON(map[string]any{
			"scope": req.Scope, "fields": changed, "mode": res.Mode, "revision": res.Revision, "pullRequestUrl": res.PullRequestURL,
		})})

	out := configPutResponse{Mode: res.Mode, Revision: res.Revision, PullRequestURL: res.PullRequestURL}
	if res.Mode == writeback.ModeCommit {
		s.rt.Trigger()
		if nr, ok := s.waitActivation(r.Context(), rev.SHA, res.SHA, prevErr); ok {
			out.Revision = nr.ID
		}
	} else {
		out.Revision = ""
	}
	writeJSON(w, http.StatusOK, out)
}

// waitActivation waits until a revision other than before (or exactly want, when set) is
// active. It gives up when the runtime reports a new error (rejected revision).
func (s *server) waitActivation(ctx context.Context, before, want, prevErr string) (rev struct{ ID, SHA string }, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, s.opts.ActivationTimeout)
	defer cancel()
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		if cur, has := s.rt.Revision(); has {
			if (want != "" && cur.SHA == want) || (want == "" && cur.SHA != before) {
				return struct{ ID, SHA string }{cur.ID, cur.SHA}, true
			}
		}
		if e := s.rt.Status().LastError; e != "" && e != prevErr {
			return rev, false
		}
		select {
		case <-ctx.Done():
			return rev, false
		case <-t.C:
		}
	}
}
