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
// it for all of them in one write-back, with each instance's grants brought in line with
// the new version (see UpdateGrants) and, for registry plugins, its version range widened
// when the new version is outside it.

// UpdateSpec is the version an update installs.
type UpdateSpec struct {
	SecretDeclarations []config.PluginSecret
	// Source is "<registry>/<plugin>", or "cicord/<plugin>" for a copy of an official plugin.
	Source                     string
	Name, DisplayName, Version string
	Capabilities, Intents      []string
	PrivilegedIntents, Network []string
	// Optional are the optional features of the new version (their capabilities stay
	// granted to the instances that grant them).
	Optional []config.OptionalFeature
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

// PermissionDiff compares what the new version requests with the installed one. Added
// lists what the new version requires and the installed one did not; Removed what the new
// version no longer declares at all (neither required nor in an optional feature).
type PermissionDiff struct {
	Capabilities ListDiff `json:"capabilities"`
	// MadeOptional: capabilities the installed version required that the new one declares
	// in optional features only. Instances that grant them keep them.
	MadeOptional []OptionalGrant `json:"madeOptional"`
	Intents      ListDiff        `json:"intents"`
	// IntentsMadeOptional: the same for intents (requested while their feature is granted).
	IntentsMadeOptional []OptionalGrant `json:"intentsMadeOptional"`
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
	// Optional are the optional features of the installed version (when its manifest is
	// loaded).
	Optional []config.OptionalFeature `json:"optional,omitempty"`
}

// OptionalGrant is a capability (or intent) declared by optional features.
type OptionalGrant struct {
	Name     string   `json:"name"`
	Features []string `json:"features"`
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
	// GrantsAdded and GrantsRemoved change the instance's grants (bot.yaml): Added are
	// capabilities the new version requires, Removed grants it no longer declares.
	GrantsAdded   []string `json:"grantsAdded"`
	GrantsRemoved []string `json:"grantsRemoved"`
	// GrantsKept are the instance's grants kept for an optional feature of the new version
	// (not required by it).
	GrantsKept []OptionalGrant `json:"grantsKept"`
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
		plan.Current = InstalledVersion{Version: cur.Metadata.Version, Capabilities: nonNil(cur.Spec.Capabilities), Intents: nonNil(cur.Spec.Intents), Network: []string{},
			Optional: cur.Spec.Optional}
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
	plan.Diff = permissionDiff(plan.Current, spec)

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
		grants := UpdateGrants(u.inst.Grants, spec)
		// Minimal edits (writeback.PatchYAML): the rest of bot.yaml keeps its layout.
		if newRange != "" {
			r := newRange
			botEdits[u.bot.File] = append(botEdits[u.bot.File], func(cur []byte) ([]byte, error) {
				return writeback.PatchYAML(cur, append(slices.Clone(at), writeback.Key("version")), r)
			})
		}
		if !slices.Equal(grants.Grants, u.inst.Grants) {
			caps := slices.Clone(grants.Grants)
			botEdits[u.bot.File] = append(botEdits[u.bot.File], func(cur []byte) ([]byte, error) {
				var v any = caps
				if len(caps) == 0 {
					v = nil
				}
				return writeback.PatchYAML(cur, append(slices.Clone(at), writeback.Key("grants")), v)
			})
		}
		on := u.inst.Servers
		if len(on) == 0 {
			on = u.bot.Spec.ServerKeys()
		}
		for _, sk := range on {
			servers[sk] = true
			ui := UpdateInstance{Server: sk, Bot: u.key, Instance: u.inst.Name, NewRange: newRange,
				GrantsAdded: grants.Added, GrantsRemoved: grants.Removed, GrantsKept: grants.Kept, Diagnostics: config.Diagnostics{}}
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
			if !config.Disabled(cfg) && !slices.Contains(plan.bots, u.key) {
				plan.bots = append(plan.bots, u.key)
			}
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
	// Version is the installed version (a single plugin; "" for several).
	Version        string                 `json:"version"`
	Mode           string                 `json:"mode"`
	Revision       string                 `json:"revision,omitempty"`
	PullRequestURL string                 `json:"pullRequestUrl,omitempty"`
	Message        string                 `json:"message,omitempty"`
	Instances      []UpdateInstanceHealth `json:"instances"`
	// Updated lists the plugins of the change, by source.
	Updated []UpdatedPlugin `json:"updated"`
	// Warnings: what the change could not keep up to date (the portal's route types).
	Warnings []string `json:"warnings,omitempty"`
}

// UpdatedPlugin is one plugin of an update.
type UpdatedPlugin struct {
	Source string `json:"source"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// UpdateInstanceHealth is the health of an updated instance once the revision is active.
type UpdateInstanceHealth struct {
	Server   string `json:"server"`
	Bot      string `json:"bot"`
	Instance string `json:"instance"`
	Health   string `json:"health"`
	Message  string `json:"message,omitempty"`
}

// Update applies a plan computed against the active configuration (UpdateAll of one plan).
func (in *Installer) Update(ctx context.Context, plan *UpdatePlan, source, actor string) (UpdateResult, error) {
	return in.UpdateAll(ctx, []*UpdatePlan{plan}, source, actor)
}

// UpdateAll applies plans computed against the active configuration, one per plugin, as
// one change (one commit or one pull request): privileged intents check, validation on a
// copy of the active revision with every update applied (plugins built, portal route types
// regenerated once), write-back and, in commit mode, the wait for the new revision. It
// applies all of them or none.
func (in *Installer) UpdateAll(ctx context.Context, plans []*UpdatePlan, source, actor string) (UpdateResult, error) {
	rt := in.Runtime
	repo := rt.Repo()
	rev, ok := rt.Revision()
	if repo == nil || !ok {
		return UpdateResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "No active configuration yet.")
	}
	if len(plans) == 0 {
		return UpdateResult{}, errorf(http.StatusBadRequest, "invalid", "", "Nothing to update.")
	}
	plans = slices.Clone(plans)
	slices.SortFunc(plans, func(a, b *UpdatePlan) int { return strings.Compare(a.Spec.Source, b.Spec.Source) })
	for i, plan := range plans {
		if plan.baseRepo != nil && plan.baseRepo != repo {
			return UpdateResult{}, errorf(http.StatusConflict, "changed", "", "The active configuration changed. Review the update again.")
		}
		if i > 0 && plans[i-1].Spec.Source == plan.Spec.Source {
			return UpdateResult{}, errorf(http.StatusBadRequest, "invalid", "", "%s is listed twice.", plan.Spec.Source)
		}
	}
	if in.Writer == nil {
		return UpdateResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "Write-back is not configured: plugins cannot be updated from the admin.")
	}
	for _, plan := range plans {
		for _, bk := range plan.bots {
			if b := repo.Bots[bk]; b != nil {
				if err := in.checkIntents(ctx, b, plan.Spec.DisplayName, plan.Spec.PrivilegedIntents); err != nil {
					return UpdateResult{}, err
				}
			}
		}
	}
	edits := MergeEdits(plans)
	ds, routes, err := in.validate(rev.Dir, edits)
	if err != nil {
		return UpdateResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: "The update cannot be applied to the configuration.", Hint: "See the details.", Details: err.Error()}
	} else if ds.HasErrors() {
		msgs := make([]string, 0, len(ds.Errors()))
		for _, d := range ds.Errors() {
			msgs = append(msgs, d.Message)
		}
		with := "these updates"
		if len(plans) == 1 {
			with = plans[0].Spec.DisplayName + " " + plans[0].Spec.Version
		}
		return UpdateResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message:     "The configuration is not valid with " + with + ": " + msgs[0],
			Hint:        "Fix the configuration of the instances (see the details), then update again.",
			Diagnostics: ds.Errors(), Details: strings.Join(msgs, "\n")}
	}

	msg := routes.apply(edits, updateMessage(plans))
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
	out := UpdateResult{Mode: res.Mode, PullRequestURL: res.PullRequestURL, Instances: []UpdateInstanceHealth{},
		Updated: make([]UpdatedPlugin, 0, len(plans)), Warnings: routes.Warnings}
	for _, plan := range plans {
		out.Updated = append(out.Updated, UpdatedPlugin{Source: plan.Spec.Source, From: plan.Current.Version, To: plan.Spec.Version})
	}
	if len(plans) == 1 {
		out.Version = plans[0].Spec.Version
	}
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
			for _, plan := range plans {
				out.Instances = append(out.Instances, updatedHealth(rt, plan)...)
			}
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

// MergeEdits combines the edits of several plans (sorted by source) into one change. A
// file several plans edit (a bot.yaml, cicord.lock) gets their edits in turn: each edits
// its own plugin's entries, and the cicord.lock edit of each checks its own entry.
func MergeEdits(plans []*UpdatePlan) map[string]writeback.EditFunc {
	out := map[string]writeback.EditFunc{}
	for _, plan := range plans {
		for _, path := range slices.Sorted(maps.Keys(plan.Edits)) {
			next := plan.Edits[path]
			prev, ok := out[path]
			if !ok {
				out[path] = next
				continue
			}
			out[path] = func(cur []byte) ([]byte, error) {
				mid, err := prev(cur)
				if err != nil {
					return nil, err
				}
				return next(mid)
			}
		}
	}
	return out
}

// updateMessage is the commit message (and pull request description) of an update.
func updateMessage(plans []*UpdatePlan) string {
	var servers []string
	for _, plan := range plans {
		for _, k := range plan.Servers {
			if !slices.Contains(servers, k) {
				servers = append(servers, k)
			}
		}
	}
	sort.Strings(servers)
	granted := func(plan *UpdatePlan) string {
		if len(plan.Diff.Capabilities.Added) == 0 {
			return ""
		}
		return "\nNewly granted: " + strings.Join(plan.Diff.Capabilities.Added, ", ") + "."
	}
	if len(plans) == 1 {
		p := plans[0]
		return fmt.Sprintf("plugin: update %s %s → %s\n\nUpdated on %s.%s", p.Spec.Source, p.Current.Version, p.Spec.Version,
			strings.Join(servers, ", "), granted(p))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "plugin: update %d plugins\n", len(plans))
	for _, p := range plans {
		fmt.Fprintf(&b, "\n%s %s → %s%s\n", p.Spec.Source, p.Current.Version, p.Spec.Version, granted(p))
	}
	fmt.Fprintf(&b, "\nUpdated on %s.", strings.Join(servers, ", "))
	return b.String()
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

// GrantChange is what an update does to an instance's grants.
type GrantChange struct {
	// Grants is the new list: the current grants the new version still declares, in their
	// order, then the capabilities it newly requires.
	Grants []string
	// Added: newly required capabilities; Removed: grants no longer declared (sorted).
	Added, Removed []string
	// Kept: current grants kept for an optional feature only.
	Kept []OptionalGrant
}

// UpdateGrants computes an instance's grants after an update to spec. A grant is kept
// when the new version still declares it, required or in any optional feature (an
// optional feature that is configured keeps working); it is dropped only when the new
// version no longer declares it at all. Capabilities the new version requires that are
// not granted yet are added. Matching follows the loader (config.GrantDeclared,
// config.HasGrant): a wildcard grant such as plugins.query:xp.* is kept while it covers a
// declared capability.
func UpdateGrants(current []string, spec UpdateSpec) GrantChange {
	declared := slices.Clone(spec.Capabilities)
	for _, f := range spec.Optional {
		declared = append(declared, f.Capabilities...)
	}
	out := GrantChange{Grants: []string{}, Added: []string{}, Removed: []string{}, Kept: []OptionalGrant{}}
	for _, g := range current {
		if !config.GrantDeclared(declared, g) {
			if !slices.Contains(out.Removed, g) {
				out.Removed = append(out.Removed, g)
			}
			continue
		}
		out.Grants = append(out.Grants, g)
		if config.GrantDeclared(spec.Capabilities, g) || slices.ContainsFunc(out.Kept, func(k OptionalGrant) bool { return k.Name == g }) {
			continue
		}
		var features []string
		for _, f := range spec.Optional {
			if config.GrantDeclared(f.Capabilities, g) {
				features = append(features, f.Feature)
			}
		}
		out.Kept = append(out.Kept, OptionalGrant{Name: g, Features: features})
	}
	for _, c := range spec.Capabilities {
		if !config.HasGrant(out.Grants, c) {
			out.Grants = append(out.Grants, c)
			out.Added = append(out.Added, c)
		}
	}
	slices.Sort(out.Added)
	slices.Sort(out.Removed)
	return out
}

// permissionDiff compares the installed version with the new one (see PermissionDiff).
func permissionDiff(cur InstalledVersion, spec UpdateSpec) PermissionDiff {
	curCaps, curIntents := config.PluginSpec{Capabilities: cur.Capabilities, Intents: cur.Intents, Optional: cur.Optional}.Declared()
	newCaps, newIntents := config.PluginSpec{Capabilities: spec.Capabilities, Intents: spec.Intents, Optional: spec.Optional}.Declared()
	removed := func(before, after []string) []string { return diff(before, after).Removed }
	featuresOf := func(name string, intents bool) []string {
		var out []string
		for _, f := range spec.Optional {
			list := f.Capabilities
			if intents {
				list = f.Intents
			}
			if slices.Contains(list, name) {
				out = append(out, f.Feature)
			}
		}
		return out
	}
	madeOptional := func(required, newRequired, newDeclared []string, intents bool) []OptionalGrant {
		out := []OptionalGrant{}
		for _, x := range diff(required, newRequired).Removed {
			if slices.Contains(newDeclared, x) {
				out = append(out, OptionalGrant{Name: x, Features: featuresOf(x, intents)})
			}
		}
		return out
	}
	return PermissionDiff{
		Capabilities:        ListDiff{Added: diff(cur.Capabilities, spec.Capabilities).Added, Removed: removed(curCaps, newCaps)},
		MadeOptional:        madeOptional(cur.Capabilities, spec.Capabilities, newCaps, false),
		Intents:             ListDiff{Added: diff(cur.Intents, spec.Intents).Added, Removed: removed(curIntents, newIntents)},
		IntentsMadeOptional: madeOptional(cur.Intents, spec.Intents, newIntents, true),
		PrivilegedIntents:   diff(cur.PrivilegedIntents, spec.PrivilegedIntents),
		Network:             diff(cur.Network, spec.Network),
	}
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
