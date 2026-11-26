package setup

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/writeback"
)

// Plugin updates (Store lifecycle: browse → install → update → uninstall).
//
// A plugin version is shared by every instance using it: the cicord.lock entry of a
// registry plugin, or the plugins/<name>/ copy of an official plugin. An update changes
// it for all of them in one write-back, with each instance's grants set to what the new
// version requests and, for registry plugins, its version range widened when the new
// version is outside it.

// UpdateSpec is the version an update installs.
type UpdateSpec struct {
	SecretDeclarations []config.PluginSecret
	// Source is "<registry>/<plugin>", or "cicord/<plugin>" for a copy of an official plugin.
	Source                     string
	Name, DisplayName, Version string
	Capabilities, Intents      []string
	PrivilegedIntents, Network []string
	// Schema is the config schema of the new version (nil: none).
	Schema map[string]any
	// Lock is the cicord.lock entry of a registry plugin.
	Lock *registry.LockEntry
	// Files are the files of a bundled plugin, relative to its directory.
	Files map[string][]byte
	// Range, when set, is written as the version range of every instance (CLI --to).
	Range string
}

// Bundled reports whether the update rewrites a copy of an official plugin.
func (u UpdateSpec) Bundled() bool { return u.Lock == nil }

// ListDiff is what a new version adds to and removes from a list.
type ListDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// Empty reports whether nothing changes.
func (d ListDiff) Empty() bool { return len(d.Added) == 0 && len(d.Removed) == 0 }

// PermissionDiff compares what the new version requests with the installed one.
type PermissionDiff struct {
	Capabilities ListDiff `json:"capabilities"`
	Intents      ListDiff `json:"intents"`
	// PrivilegedIntents must be enabled in the Discord developer portal.
	PrivilegedIntents ListDiff `json:"privilegedIntents"`
	Network           ListDiff `json:"network"`
}

// InstalledVersion is what is installed now.
type InstalledVersion struct {
	Version           string   `json:"version"`
	Commit            string   `json:"commit,omitempty"`
	Integrity         string   `json:"integrity,omitempty"`
	Repo              string   `json:"repo,omitempty"`
	Capabilities      []string `json:"capabilities"`
	Intents           []string `json:"intents"`
	PrivilegedIntents []string `json:"privilegedIntents"`
	Network           []string `json:"network"`
}

// UpdateInstance is what the update changes for one instance on one server.
type UpdateInstance struct {
	SecretChanges []SecretChange `json:"secretChanges,omitempty"`
	Server        string         `json:"server"`
	Bot           string         `json:"bot"`
	Instance      string         `json:"instance"`
	// Range is the instance's version range (registry plugins) and NewRange the one the
	// update writes ("" when it stays).
	Range    string `json:"range,omitempty"`
	NewRange string `json:"newRange,omitempty"`
	// GrantsAdded and GrantsRemoved change the instance's grants (bot.yaml).
	GrantsAdded   []string `json:"grantsAdded"`
	GrantsRemoved []string `json:"grantsRemoved"`
	// Diagnostics: the instance's current config is not valid for the new version's
	// config schema (it must be fixed first).
	Diagnostics config.Diagnostics `json:"diagnostics"`
}

// FileChanges lists what a bundled update does to plugins/<name>/ (plugin-relative paths).
type FileChanges struct {
	Changed []string `json:"changed"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// UpdatePlan is an update computed against a repository: the review shown to the admin
// and the edits that apply it.
type UpdatePlan struct {
	SecretReview string `json:"secretReview,omitempty"`
	baseRepo     *config.Repo
	Spec         UpdateSpec       `json:"-"`
	Current      InstalledVersion `json:"current"`
	Diff         PermissionDiff   `json:"diff"`
	Instances    []UpdateInstance `json:"instances"`
	// Files: bundled updates only.
	Files *FileChanges `json:"files,omitempty"`
	// Modified: the bundled copy was changed locally (see catalog.CopyStatus).
	Modified bool `json:"modified"`
	// RepoChanged: the registry index now points to another repository than cicord.lock.
	RepoChanged bool `json:"repoChanged"`
	// Servers where an instance of the plugin runs (an update needs admin on each).
	Servers []string `json:"servers"`

	Edits map[string]writeback.EditFunc `json:"-"`
	bots  []string
}

// ConfigValid reports whether every instance's config is valid for the new version.
func (p *UpdatePlan) ConfigValid() bool {
	for _, i := range p.Instances {
		if i.Diagnostics.HasErrors() {
			return false
		}
	}
	return true
}

// ErrNotInstalled: no instance uses the plugin.
var ErrNotInstalled = errors.New("plugin not installed")

// UsesPlugin reports whether a bot.yaml instance uses the plugin of an update source:
// "<registry>/<plugin>" locked in cicord.lock (the official registry included), or for
// "cicord/<plugin>" also a copy of the official plugin in plugins/<plugin>/.
func UsesPlugin(b *config.Bot, p config.PluginInstance, source string) bool {
	return usesCopy(b, p, source) || UsesLocked(p, source)
}

// UsesLocked: the instance uses the registry plugin source (a cicord.lock entry).
func UsesLocked(p config.PluginInstance, source string) bool {
	return !p.IsLocal() && p.Source == source
}

func usesCopy(b *config.Bot, p config.PluginInstance, source string) bool {
	reg, name, _ := strings.Cut(source, "/")
	return reg == config.OfficialRegistryName && p.IsLocal() &&
		path.Clean(path.Join(path.Dir(b.File), p.Source)) == catalog.PluginDir(name)
}

// LockedSource reports whether an instance of repo uses source through cicord.lock (for
// "cicord/<plugin>": the official remote registry rather than a bundled copy).
func LockedSource(repo *config.Repo, source string) bool {
	if repo == nil {
		return false
	}
	for _, b := range repo.Bots {
		for _, p := range b.Spec.Plugins {
			if UsesLocked(p, source) {
				return true
			}
		}
	}
	return false
}

// PlanUpdate computes the update of every instance using spec.Source in repo (the active
// configuration). It does not evaluate any plugin code.
func PlanUpdate(repo *config.Repo, spec UpdateSpec) (*UpdatePlan, error) {
	if repo == nil {
		return nil, errors.New("no configuration")
	}
	plan := &UpdatePlan{baseRepo: repo, Spec: spec, Instances: []UpdateInstance{}, Servers: []string{}, Edits: map[string]writeback.EditFunc{}}
	newLP, err := config.ConfigSchemaPlugin(spec.Schema)
	if err != nil {
		return nil, fmt.Errorf("config schema of %s %s: %w", spec.Name, spec.Version, err)
	}

	newLP.Manifest = &config.Plugin{Spec: config.PluginSpec{Secrets: spec.SecretDeclarations}}
	type use struct {
		bot  *config.Bot
		key  string
		idx  int
		inst config.PluginInstance
	}
	var uses []use
	for _, bk := range sortedKeys(repo.Bots) {
		b := repo.Bots[bk]
		for i, p := range b.Spec.Plugins {
			if (spec.Bundled() && usesCopy(b, p, spec.Source)) || (!spec.Bundled() && UsesLocked(p, spec.Source)) {
				uses = append(uses, use{bot: b, key: bk, idx: i, inst: p})
			}
		}
	}
	if len(uses) == 0 {
		return nil, ErrNotInstalled
	}

	// What is installed: the loaded manifest of an instance (no download).
	var cur *config.Plugin
	for _, in := range repo.Instances {
		if in.Plugin != nil && in.Plugin.Manifest != nil && slices.ContainsFunc(uses, func(u use) bool { return u.key == in.Bot && u.inst.Name == in.Name }) {
			cur = in.Plugin.Manifest
			break
		}
	}
	if cur != nil {
		plan.Current = InstalledVersion{Version: cur.Metadata.Version, Capabilities: nonNil(cur.Spec.Capabilities), Intents: nonNil(cur.Spec.Intents), Network: []string{}}
		if cur.Spec.Network != nil {
			plan.Current.Network = nonNil(cur.Spec.Network.Allow)
		}
	}
	var lockCur *registry.LockEntry
	if !spec.Bundled() {
		lock, err := registry.ReadLock(repo.Root)
		if err != nil {
			return nil, err
		}
		if e, ok := lock.Plugins[spec.Source]; ok {
			lockCur = &e
			if plan.Current.Version == "" {
				plan.Current = InstalledVersion{Version: e.Version, Capabilities: nonNil(e.Capabilities), Intents: []string{}, Network: []string{}}
			}
			plan.Current.Commit, plan.Current.Integrity, plan.Current.Repo = e.Commit, e.Integrity, registry.Redact(e.Repo)
			plan.RepoChanged = config.NormalizeRegistryURL(e.Repo) != config.NormalizeRegistryURL(spec.Lock.Repo) || e.Path != spec.Lock.Path
		}
	}
	plan.Current.PrivilegedIntents = privileged(plan.Current.Intents)
	plan.Diff = PermissionDiff{
		Capabilities:      diff(plan.Current.Capabilities, spec.Capabilities),
		Intents:           diff(plan.Current.Intents, spec.Intents),
		PrivilegedIntents: diff(plan.Current.PrivilegedIntents, spec.PrivilegedIntents),
		Network:           diff(plan.Current.Network, spec.Network),
	}

	current := map[string]*config.Instance{}
	for n := range repo.Instances {
		instance := &repo.Instances[n]
		current[instance.ID()] = instance
	}
	var review []secretReviewEntry
	// Per instance: version range, grants, config validity on each server.
	servers := map[string]bool{}
	botEdits := map[string][]func([]byte) ([]byte, error){}
	for _, u := range uses {
		at := []writeback.PathElem{writeback.Key("spec"), writeback.Key("plugins"), writeback.Match("name", u.inst.Name)}
		newRange := ""
		if !spec.Bundled() {
			switch {
			case spec.Range != "" && spec.Range != u.inst.Version:
				newRange = spec.Range
			case spec.Range == "" && !registry.RangeAllows(u.inst.Version, spec.Version):
				newRange = "^" + spec.Version
			}
		}
		grants := diff(u.inst.Grants, spec.Capabilities)
		if newRange != "" {
			r := newRange
			botEdits[u.bot.File] = append(botEdits[u.bot.File], func(cur []byte) ([]byte, error) {
				return writeback.SetYAML(cur, append(slices.Clone(at), writeback.Key("version")), r)
			})
		}
		if !grants.Empty() || len(u.inst.Grants) != len(spec.Capabilities) {
			caps := slices.Clone(spec.Capabilities)
			botEdits[u.bot.File] = append(botEdits[u.bot.File], func(cur []byte) ([]byte, error) {
				var v any = caps
				if len(caps) == 0 {
					v = nil
				}
				return writeback.SetYAML(cur, append(slices.Clone(at), writeback.Key("grants")), v)
			})
		}
		if !slices.Contains(plan.bots, u.key) {
			plan.bots = append(plan.bots, u.key)
		}
		on := u.inst.Servers
		if len(on) == 0 {
			on = u.bot.Spec.ServerKeys()
		}
		for _, sk := range on {
			servers[sk] = true
			ui := UpdateInstance{Server: sk, Bot: u.key, Instance: u.inst.Name, NewRange: newRange,
				GrantsAdded: grants.Added, GrantsRemoved: grants.Removed, Diagnostics: config.Diagnostics{}}
			if !spec.Bundled() {
				ui.Range = u.inst.Version
			}
			base, _ := config.NormalizeConfig(u.inst.Config)
			var override map[string]any
			if o, ok := u.inst.Overrides[sk]; ok && o.Config != nil {
				override, _ = config.NormalizeConfig(o.Config)
			}
			var srv *config.ServerSpec
			if s := repo.Servers[sk]; s != nil {
				srv = &s.Spec
			}
			cfg := config.EffectiveConfig(newLP, base, override)
			if ds := config.ValidateInstanceConfig(newLP, cfg, sk, srv, u.bot.File, fmt.Sprintf("/spec/plugins/%d/config", u.idx)); len(ds) > 0 {
				ui.Diagnostics = ds
			}
			for name := range u.inst.Secrets {
				if !slices.ContainsFunc(spec.SecretDeclarations, func(s config.PluginSecret) bool { return s.Name == name }) {
					ui.Diagnostics = append(ui.Diagnostics, config.Diagnostic{Severity: config.SeverityError, File: u.bot.File, Path: fmt.Sprintf("/spec/plugins/%d/secrets/%s", u.idx, name), Message: fmt.Sprintf("the new plugin no longer declares bound secret %s", name)})
				}
			}
			ui.SecretChanges = boundChanges(current[u.key+"/"+u.inst.Name+"/"+sk], u.inst, spec, cfg)
			for _, change := range ui.SecretChanges {
				if change.Widened {
					review = append(review, secretReviewEntry{sk, u.key, u.inst.Name, u.inst.Secrets[change.Name].SecretRef, change})
				}
			}
			plan.Instances = append(plan.Instances, ui)
		}
	}
	plan.SecretReview = secretReviewToken(spec, review)
	for sk := range servers {
		plan.Servers = append(plan.Servers, sk)
	}
	sort.Strings(plan.Servers)
	for file, fns := range botEdits {
		plan.Edits[file] = func(cur []byte) ([]byte, error) {
			if cur == nil {
				return nil, fmt.Errorf("%s does not exist", file)
			}
			var err error
			for _, fn := range fns {
				if cur, err = fn(cur); err != nil {
					return nil, err
				}
			}
			return cur, nil
		}
	}

	if spec.Bundled() {
		dir := catalog.PluginDir(spec.Name)
		copied, err := catalog.ReadDir(filepath.Join(repo.Root, filepath.FromSlash(dir)))
		if err != nil {
			return nil, err
		}
		fc := &FileChanges{Changed: []string{}, Added: []string{}, Removed: []string{}}
		for _, p := range catalog.SortedPaths(spec.Files) {
			data := spec.Files[p]
			old, ok := copied[p]
			switch {
			case !ok:
				fc.Added = append(fc.Added, p)
			case string(old) != string(data):
				fc.Changed = append(fc.Changed, p)
			default:
				continue
			}
			plan.Edits[dir+"/"+p] = func([]byte) ([]byte, error) { return data, nil }
		}
		for _, p := range catalog.SortedPaths(copied) {
			if _, ok := spec.Files[p]; !ok {
				fc.Removed = append(fc.Removed, p)
				plan.Edits[dir+"/"+p] = func([]byte) ([]byte, error) { return nil, nil }
			}
		}
		plan.Files = fc
	} else {
		next, want := *spec.Lock, lockCur
		plan.Edits[registry.LockFile] = func(cur []byte) ([]byte, error) {
			l, err := registry.ParseLock(cur)
			if err != nil {
				return nil, err
			}
			// The lock must still be what the update was computed from.
			if e, ok := l.Plugins[spec.Source]; want != nil && (!ok || e.Commit != want.Commit) {
				return nil, fmt.Errorf("%s: %s changed meanwhile (now %s)", registry.LockFile, spec.Source, e.Version)
			}
			return registry.SetLockEntry(cur, spec.Source, next)
		}
	}
	return plan, nil
}

// UpdateResult is the outcome of an update.
type UpdateResult struct {
	Version        string                 `json:"version"`
	Mode           string                 `json:"mode"`
	Revision       string                 `json:"revision,omitempty"`
	PullRequestURL string                 `json:"pullRequestUrl,omitempty"`
	Message        string                 `json:"message,omitempty"`
	Instances      []UpdateInstanceHealth `json:"instances"`
	// Warnings: what the change could not keep up to date (the portal's route types).
	Warnings []string `json:"warnings,omitempty"`
}

// UpdateInstanceHealth is the health of an updated instance once the revision is active.
type UpdateInstanceHealth struct {
	Server   string `json:"server"`
	Bot      string `json:"bot"`
	Instance string `json:"instance"`
	Health   string `json:"health"`
	Message  string `json:"message,omitempty"`
}

// Update applies a plan computed against the active configuration: privileged intents
// check, validation on a copy of the active revision (plugins built), write-back and, in
// commit mode, the wait for the new revision.
func (in *Installer) Update(ctx context.Context, plan *UpdatePlan, source, actor string) (UpdateResult, error) {
	rt := in.Runtime
	repo := rt.Repo()
	rev, ok := rt.Revision()
	if repo == nil || !ok {
		return UpdateResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "No active configuration yet.")
	}
	if plan.baseRepo != nil && plan.baseRepo != repo {
		return UpdateResult{}, errorf(http.StatusConflict, "changed", "", "The active configuration changed. Review the update again.")
	}
	if in.Writer == nil {
		return UpdateResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "Write-back is not configured: plugins cannot be updated from the admin.")
	}
	spec := plan.Spec
	for _, bk := range plan.bots {
		if b := repo.Bots[bk]; b != nil {
			if err := in.checkIntents(ctx, b, spec.DisplayName, spec.PrivilegedIntents); err != nil {
				return UpdateResult{}, err
			}
		}
	}
	edits := maps.Clone(plan.Edits)
	ds, routes, err := in.validate(rev.Dir, edits)
	if err != nil {
		return UpdateResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: "The update cannot be applied to the configuration.", Hint: "See the details.", Details: err.Error()}
	} else if ds.HasErrors() {
		msgs := make([]string, 0, len(ds.Errors()))
		for _, d := range ds.Errors() {
			msgs = append(msgs, d.Message)
		}
		return UpdateResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message:     "The configuration is not valid with " + spec.DisplayName + " " + spec.Version + ": " + msgs[0],
			Hint:        "Fix the configuration of the instances (see the details), then update again.",
			Diagnostics: ds.Errors(), Details: strings.Join(msgs, "\n")}
	}

	added := ""
	if len(plan.Diff.Capabilities.Added) > 0 {
		added = "\nNewly granted: " + strings.Join(plan.Diff.Capabilities.Added, ", ") + "."
	}
	msg := routes.apply(edits, fmt.Sprintf("plugin: update %s %s → %s\n\nUpdated on %s.%s", spec.Source, plan.Current.Version, spec.Version,
		strings.Join(plan.Servers, ", "), added))
	mode := writeback.ModeFor(repo.Project.Spec.WriteBack, editPaths(edits))
	prevErr := rt.Status().LastError
	res, err := in.Writer.Write(ctx, writeback.Request{Edits: edits, Message: msg, Actor: actor, Source: source, Mode: mode})
	if err != nil {
		if errors.Is(err, writeback.ErrConflict) {
			return UpdateResult{}, &Error{Status: http.StatusConflict, Code: "conflict", Message: "The repository changed meanwhile.", Hint: "Review the update again.", Details: err.Error()}
		}
		return UpdateResult{}, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Could not save the update to the repository.",
			Hint: "Check the repository access (Diagnostics → write-back), then try again.", Details: err.Error()}
	}
	out := UpdateResult{Version: spec.Version, Mode: res.Mode, PullRequestURL: res.PullRequestURL, Instances: []UpdateInstanceHealth{},
		Warnings: routes.Warnings}
	if out.Mode == "" {
		out.Mode = mode
	}
	if out.Mode != writeback.ModeCommit {
		out.Message = "A pull request was opened: the update is applied once it is merged."
		return out, nil
	}
	rt.Trigger()
	timeout := in.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if cur, ok := rt.Revision(); ok && cur.SHA != rev.SHA {
			out.Revision = cur.ID
			out.Instances = updatedHealth(rt, plan)
			return out, nil
		}
		if e := rt.Status().LastError; e != "" && e != prevErr {
			return out, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid", Message: "The runtime rejected the new configuration.",
				Hint: "See the details; the previous version stays active.", Details: e}
		}
		select {
		case <-wctx.Done():
			out.Message = "Saved; the runtime has not activated it yet."
			return out, nil
		case <-tick.C:
		}
	}
}

func updatedHealth(rt *runtime.Runtime, plan *UpdatePlan) []UpdateInstanceHealth {
	out := []UpdateInstanceHealth{}
	for _, ui := range plan.Instances {
		h := UpdateInstanceHealth{Server: ui.Server, Bot: ui.Bot, Instance: ui.Instance, Health: runtime.HealthProgressing}
		for _, i := range rt.Instances(ui.Server) {
			if i.Config.Bot == ui.Bot && i.Config.Name == ui.Instance {
				h.Health, h.Message = i.Health, i.Message
			}
		}
		out = append(out, h)
	}
	return out
}

// diff lists what b adds to a and removes from it (sorted).
func diff(a, b []string) ListDiff {
	d := ListDiff{Added: []string{}, Removed: []string{}}
	for _, x := range b {
		if !slices.Contains(a, x) && !slices.Contains(d.Added, x) {
			d.Added = append(d.Added, x)
		}
	}
	for _, x := range a {
		if !slices.Contains(b, x) && !slices.Contains(d.Removed, x) {
			d.Removed = append(d.Removed, x)
		}
	}
	slices.Sort(d.Added)
	slices.Sort(d.Removed)
	return d
}

func privileged(intents []string) []string {
	in, err := discord.ParseIntents(intents)
	if err != nil {
		return []string{}
	}
	return nonNil((in & discord.PrivilegedIntents).Names())
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return slices.Clone(s)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PluginServers lists the servers where an instance of the plugin of an update source
// runs (sorted; empty when it is not installed).
func PluginServers(repo *config.Repo, source string) []string {
	seen := map[string]bool{}
	if repo != nil {
		for _, b := range repo.Bots {
			for _, p := range b.Spec.Plugins {
				if !UsesPlugin(b, p, source) {
					continue
				}
				on := p.Servers
				if len(on) == 0 {
					on = b.Spec.ServerKeys()
				}
				for _, s := range on {
					seen[s] = true
				}
			}
		}
	}
	return sortedKeys(seen)
}
