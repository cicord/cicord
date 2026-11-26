package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/writeback"
)

// Installer installs plugins into the active repository through write-back, from the
// bundled catalog (sources copied to plugins/<name>/) or from a registry (cicord.lock entry
// + `source: <registry>/<name>`). The instance is appended to the bot's spec.plugins with
// every capability the plugin declares granted (as reviewed by the person installing).
type Installer struct {
	Runtime *runtime.Runtime
	Writer  writeback.Writer
	Catalog *catalog.Catalog
	Probe   Probe
	Secrets func(string) (string, bool)
	// Timeout bounds the wait for the new revision (default 30s).
	Timeout time.Duration
}

// Package is what an install adds to the repository.
type Package struct {
	Name, DisplayName, Version string
	// From names where it comes from, for the commit message ("the cicord catalog").
	From              string
	Capabilities      []string
	PrivilegedIntents []string
	FirstCommand      string
	// Bundled plugin: its files (repo-relative paths under plugins/<name>/).
	Files map[string][]byte
	// Registry plugin: "<registry>/<name>", the version range and the lock entry.
	Source       string
	VersionRange string
	Lock         *registry.LockEntry
}

// Target is where a package is installed.
type Target struct {
	Server, Bot string
	// Instance name (default: the plugin name).
	Instance string
	Config   map[string]any
}

// Install installs the catalog plugin req.Plugin for bot botKey on serverKey.
func (in *Installer) Install(ctx context.Context, serverKey, botKey string, req InstallRequest, source, actor string) (InstallResult, error) {
	entry, ok := in.Catalog.Get(req.Plugin)
	if !ok {
		return InstallResult{}, errorf(http.StatusNotFound, "not_found", "", "Unknown plugin %q.", req.Plugin)
	}
	pkg, err := CatalogPackage(in.Catalog, entry)
	if err != nil {
		return InstallResult{}, err
	}
	return in.InstallPackage(ctx, Target{Server: serverKey, Bot: botKey, Config: req.Config}, pkg, source, actor)
}

// CatalogPackage is the package of a bundled catalog plugin.
func CatalogPackage(c *catalog.Catalog, e catalog.Entry) (Package, error) {
	files, err := c.Files(e.Name)
	if err != nil {
		return Package{}, err
	}
	pkg := Package{Name: e.Name, DisplayName: e.DisplayName, Version: e.Version, From: "the " + e.Source + " catalog",
		Capabilities: e.Capabilities, PrivilegedIntents: e.PrivilegedIntents, Files: files}
	if len(e.Commands) > 0 {
		pkg.FirstCommand = e.Commands[0].Name
	}
	return pkg, nil
}

// InstallPackage validates the change on a copy of the active revision, writes it back
// and, in commit mode, waits for the new revision to report the instance's health.
func (in *Installer) InstallPackage(ctx context.Context, t Target, pkg Package, source, actor string) (InstallResult, error) {
	rt := in.Runtime
	repo := rt.Repo()
	rev, ok := rt.Revision()
	if repo == nil || !ok {
		return InstallResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "No active configuration yet.")
	}
	if in.Writer == nil {
		return InstallResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "Write-back is not configured: plugins cannot be installed from the admin.")
	}
	instance := t.Instance
	if instance == "" {
		instance = pkg.Name
	}
	bot := repo.Bots[t.Bot]
	if bot == nil || !slices.Contains(bot.Spec.ServerKeys(), t.Server) {
		return InstallResult{}, errorf(http.StatusNotFound, "not_found", "", "Bot %q does not manage server %q.", t.Bot, t.Server)
	}
	if slices.ContainsFunc(bot.Spec.Plugins, func(p config.PluginInstance) bool { return p.Name == instance }) {
		return InstallResult{}, errorf(http.StatusConflict, "conflict", "Open Plugins in the admin to configure it, or choose another instance name.",
			"%s is already installed as %q.", pkg.DisplayName, instance)
	}

	if !config.Disabled(t.Config) {
		if err := in.checkIntents(ctx, bot, pkg.DisplayName, pkg.PrivilegedIntents); err != nil {
			return InstallResult{}, err
		}

	}
	pi := config.PluginInstance{Name: instance, Grants: slices.Clone(pkg.Capabilities)}
	edits := map[string]writeback.EditFunc{}
	switch {
	case pkg.Lock != nil:
		pi.Source, pi.Version = pkg.Source, pkg.VersionRange
		lock := *pkg.Lock
		edits[registry.LockFile] = func(cur []byte) ([]byte, error) {
			if l, err := registry.ParseLock(cur); err == nil {
				if old, ok := l.Plugins[pkg.Source]; ok && (old.Commit != lock.Commit || old.Integrity != lock.Integrity) {
					return nil, fmt.Errorf("%s is already locked at %s: update it first (the Store's update, or cicord plugin update)", pkg.Source, old.Version)
				}
			}
			return registry.SetLockEntry(cur, pkg.Source, lock)
		}
	default:
		botDir := path.Dir(bot.File)
		rel, _ := filepath.Rel(filepath.FromSlash(botDir), filepath.FromSlash(catalog.PluginDir(pkg.Name)))
		pi.Source = filepath.ToSlash(rel)
		for p, data := range pkg.Files {
			edits[p] = func(cur []byte) ([]byte, error) {
				if cur != nil && !bytes.Equal(cur, data) {
					return nil, fmt.Errorf("%s already exists with other content", p)
				}
				return data, nil
			}
		}
	}
	if len(bot.Spec.ServerKeys()) > 1 {
		pi.Servers = []string{t.Server}
	}
	if cfg := pruneEmpty(t.Config); len(cfg) > 0 {
		pi.Config = cfg
	}
	edits[bot.File] = func(cur []byte) ([]byte, error) {
		if cur == nil {
			return nil, errors.New("bot file does not exist")
		}
		return writeback.AppendYAML(cur, []writeback.PathElem{writeback.Key("spec"), writeback.Key("plugins")}, pi)
	}

	// Validate (schema, references, build, intents) on a copy of the active revision.
	ds, routes, err := in.validate(rev.Dir, edits)
	if err != nil {
		return InstallResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: "The plugin cannot be added to the configuration.", Hint: "See the details.", Details: err.Error()}
	} else if ds.HasErrors() {
		msgs := make([]string, 0, len(ds.Errors()))
		for _, d := range ds.Errors() {
			msgs = append(msgs, d.Message)
		}
		return InstallResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: "The configuration is not valid: " + msgs[0], Hint: "Fix the highlighted fields, then install again.",
			Diagnostics: ds.Errors(), Details: strings.Join(msgs, "\n")}
	}

	msg := routes.apply(edits, fmt.Sprintf("plugin(%s): install %s %s on %s\n\nInstalled from %s.", t.Bot, pkg.Name, pkg.Version, t.Server, pkg.From))
	mode := writeback.ModeFor(repo.Project.Spec.WriteBack, editPaths(edits))
	prevErr := rt.Status().LastError
	res, err := in.Writer.Write(ctx, writeback.Request{Edits: edits, Message: msg, Actor: actor, Source: source, Mode: mode})
	if err != nil {
		if errors.Is(err, writeback.ErrConflict) {
			return InstallResult{}, &Error{Status: http.StatusConflict, Code: "conflict", Message: "The repository changed meanwhile.", Hint: "Try again.", Details: err.Error()}
		}
		return InstallResult{}, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Could not save the plugin to the repository.",
			Hint: "Check the repository access (Diagnostics → write-back), then try again.", Details: err.Error()}
	}
	out := InstallResult{Instance: instance, Mode: res.Mode, PullRequestURL: res.PullRequestURL, Health: runtime.HealthProgressing, Command: pkg.FirstCommand,
		Warnings: routes.Warnings}
	if config.Disabled(t.Config) {
		out.Command = ""
	}
	if out.Mode == "" {
		out.Mode = mode
	}
	if out.Mode != writeback.ModeCommit {
		out.Message = "A pull request was opened: the plugin is active once it is merged."
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
			for _, i := range rt.Instances(t.Server) {
				if i.Config.Bot == t.Bot && i.Config.Name == instance {
					out.Health, out.Message = i.Health, i.Message
					// Commands of plugins that were not inspected before install
					// (unofficial sources) are known once the instance runs.
					if out.Command == "" && i.Definition != nil && len(i.Definition.Commands) > 0 {
						out.Command = i.Definition.Commands[0].Name
					}
					return out, nil
				}
			}
		}
		if e := rt.Status().LastError; e != "" && e != prevErr {
			return out, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid", Message: "The runtime rejected the new configuration.",
				Hint: "See the details; the previous configuration stays active.", Details: e}
		}
		select {
		case <-wctx.Done():
			out.Message = "Saved; the runtime has not activated it yet."
			return out, nil
		case <-tick.C:
		}
	}
}

// checkIntents: a plugin needing a privileged intent the bot's application has not
// enabled would make Discord refuse the bot's gateway connection (409 intents).
func (in *Installer) checkIntents(ctx context.Context, bot *config.Bot, displayName string, privileged []string) error {
	if len(privileged) == 0 || in.Probe == nil || in.Secrets == nil {
		return nil
	}
	tok, has := in.Secrets(bot.Spec.Token.SecretRef)
	if !has {
		return nil
	}
	id, err := in.Probe.Identify(ctx, tok)
	if err != nil {
		return explainProbeErr(err)
	}
	need, _ := discord.ParseIntents(privileged)
	if c := IntentsCheck(bot.Spec.ApplicationID, id.Flags, need, 0); c.Status == StatusFail {
		return &Error{Status: http.StatusConflict, Code: "intents",
			Message: fmt.Sprintf("%s needs a privileged intent that is turned off. %s", displayName, c.Message),
			Hint:    c.Hint, Link: c.Link}
	}
	return nil
}

// UninstallResult is the outcome of an uninstall.
type UninstallResult struct {
	// Removed is true when the instance was deleted; false when it keeps running on its
	// other servers (only this server was taken off its list).
	Removed bool `json:"removed"`
	// Cleaned lists what went with the last instance of the plugin: the cicord.lock entry
	// or the plugin directory.
	Cleaned        []string `json:"cleaned"`
	Mode           string   `json:"mode"`
	Revision       string   `json:"revision,omitempty"`
	PullRequestURL string   `json:"pullRequestUrl,omitempty"`
	// Warnings: what the change could not keep up to date (the portal's route types).
	Warnings []string `json:"warnings,omitempty"`
}

// Uninstall takes a plugin instance off a server. An instance that also runs on other
// servers only loses this one; otherwise it is deleted from bot.yaml and, when no other
// instance uses the plugin any more, its cicord.lock entry (registry plugin) or its
// plugins/<name>/ directory (copied plugin) goes with it. Plugin data (KV storage) is
// kept: reinstalling an instance with the same name finds it again.
func (in *Installer) Uninstall(ctx context.Context, serverKey, botKey, instance, source, actor string) (UninstallResult, error) {
	rt := in.Runtime
	repo := rt.Repo()
	rev, ok := rt.Revision()
	if repo == nil || !ok {
		return UninstallResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "No active configuration yet.")
	}
	if in.Writer == nil {
		return UninstallResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "Write-back is not configured: plugins cannot be uninstalled from the admin.")
	}
	bot := repo.Bots[botKey]
	if bot == nil || !slices.Contains(bot.Spec.ServerKeys(), serverKey) {
		return UninstallResult{}, errorf(http.StatusNotFound, "not_found", "", "Bot %q does not manage server %q.", botKey, serverKey)
	}
	idx := slices.IndexFunc(bot.Spec.Plugins, func(p config.PluginInstance) bool { return p.Name == instance })
	if idx < 0 {
		return UninstallResult{}, errorf(http.StatusNotFound, "not_found", "", "No plugin instance %q on bot %q.", instance, botKey)
	}
	pi := bot.Spec.Plugins[idx]
	servers := pi.Servers
	if len(servers) == 0 {
		servers = bot.Spec.ServerKeys()
	}
	if !slices.Contains(servers, serverKey) {
		return UninstallResult{}, errorf(http.StatusNotFound, "not_found", "", "%q does not run on %q.", instance, serverKey)
	}
	at := []writeback.PathElem{writeback.Key("spec"), writeback.Key("plugins"), writeback.Match("name", instance)}
	edits := map[string]writeback.EditFunc{}
	out := UninstallResult{Cleaned: []string{}}
	others := slices.DeleteFunc(slices.Clone(servers), func(s string) bool { return s == serverKey })
	if len(others) > 0 {
		_, hasOverride := pi.Overrides[serverKey]
		edits[bot.File] = func(cur []byte) ([]byte, error) {
			next, err := writeback.SetYAML(cur, append(slices.Clone(at), writeback.Key("servers")), others)
			if err != nil || !hasOverride {
				return next, err
			}
			// The server override of this server goes too.
			return writeback.SetYAML(next, append(slices.Clone(at), writeback.Key("overrides"), writeback.Key(serverKey)), nil)
		}
	} else {
		out.Removed = true
		edits[bot.File] = func(cur []byte) ([]byte, error) { return writeback.SetYAML(cur, at, nil) }
		// Is the plugin still used by another instance (any bot)?
		pluginKey := func(b *config.Bot, p config.PluginInstance) string {
			if p.IsLocal() {
				return path.Clean(path.Join(path.Dir(b.File), p.Source))
			}
			return p.Source
		}
		key := pluginKey(bot, pi)
		used := false
		for _, b := range repo.Bots {
			for _, p := range b.Spec.Plugins {
				if (b != bot || p.Name != instance) && pluginKey(b, p) == key {
					used = true
				}
			}
		}
		if !used {
			switch {
			case !pi.IsLocal():
				src := pi.Source
				edits[registry.LockFile] = func(cur []byte) ([]byte, error) {
					l, err := registry.ParseLock(cur)
					if err != nil {
						return nil, err
					}
					if _, ok := l.Plugins[src]; !ok {
						return cur, nil
					}
					delete(l.Plugins, src)
					return registry.MarshalLock(l)
				}
				out.Cleaned = append(out.Cleaned, registry.LockFile+": "+src)
			case strings.HasPrefix(key, "plugins/") && strings.Count(key, "/") == 1:
				// A copied plugin (Store / wizard install): delete its directory.
				files, err := listFiles(filepath.Join(rev.Dir, filepath.FromSlash(key)))
				if err != nil {
					return UninstallResult{}, err
				}
				for _, f := range files {
					edits[key+"/"+f] = func([]byte) ([]byte, error) { return nil, nil }
				}
				out.Cleaned = append(out.Cleaned, key+"/")
			}
		}
	}

	ds, routes, err := in.validate(rev.Dir, edits)
	if err != nil {
		return UninstallResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: "The plugin cannot be removed from the configuration.", Hint: "See the details.", Details: err.Error()}
	} else if ds.HasErrors() {
		msgs := make([]string, 0, len(ds.Errors()))
		for _, d := range ds.Errors() {
			msgs = append(msgs, d.Message)
		}
		return UninstallResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid",
			Message: "Removing it would break the configuration: " + msgs[0],
			Hint:    "Another plugin or setting may depend on it.", Diagnostics: ds.Errors(), Details: strings.Join(msgs, "\n")}
	}

	msg := routes.apply(edits, fmt.Sprintf("plugin(%s): uninstall %s from %s", botKey, instance, serverKey))
	mode := writeback.ModeFor(repo.Project.Spec.WriteBack, editPaths(edits))
	prevErr := rt.Status().LastError
	res, err := in.Writer.Write(ctx, writeback.Request{Edits: edits, Message: msg, Actor: actor, Source: source, Mode: mode})
	if err != nil {
		if errors.Is(err, writeback.ErrConflict) {
			return UninstallResult{}, &Error{Status: http.StatusConflict, Code: "conflict", Message: "The repository changed meanwhile.", Hint: "Try again.", Details: err.Error()}
		}
		return UninstallResult{}, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Could not save the change to the repository.",
			Hint: "Check the repository access (Diagnostics → write-back), then try again.", Details: err.Error()}
	}
	out.Mode, out.PullRequestURL, out.Warnings = res.Mode, res.PullRequestURL, routes.Warnings
	if out.Mode == "" {
		out.Mode = mode
	}
	if out.Mode != writeback.ModeCommit {
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
			return out, nil
		}
		if e := rt.Status().LastError; e != "" && e != prevErr {
			return out, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid", Message: "The runtime rejected the new configuration.",
				Hint: "See the details; the previous configuration stays active.", Details: e}
		}
		select {
		case <-wctx.Done():
			return out, nil
		case <-tick.C:
		}
	}
}

// listFiles lists the regular files under dir, slash-separated and relative to it.
func listFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

// ValidateActivation checks proposed config edits against the whole installation
// (including grants, resource bindings, commands and the plugin build).
func (in *Installer) ValidateActivation(revDir string, edits map[string]writeback.EditFunc) (config.Diagnostics, error) {
	ds, _, err := in.validate(revDir, edits)
	return ds, err
}

func (in *Installer) validate(revDir string, edits map[string]writeback.EditFunc) (config.Diagnostics, portalRoutes, error) {
	tmp, err := os.MkdirTemp("", "cicord-install-*")
	if err != nil {
		return nil, portalRoutes{}, err
	}
	defer os.RemoveAll(tmp)
	if err := copyDir(revDir, tmp); err != nil {
		return nil, portalRoutes{}, err
	}
	if err := ApplyEdits(tmp, edits); err != nil {
		return nil, portalRoutes{}, err
	}
	repo, _, ds, _ := in.Runtime.LoadAndBuild(tmp)
	if ds.HasErrors() || repo == nil {
		return ds, portalRoutes{}, nil
	}
	return ds, regenerateRoutes(tmp, repo), nil
}

// ApplyEdits applies write-back edits to the files of a directory (a nil result deletes
// the file), like the writer does in the repository.
func ApplyEdits(dir string, edits map[string]writeback.EditFunc) error {
	for rel, edit := range edits {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		cur, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			cur = nil
		} else if err != nil {
			return err
		}
		next, err := edit(cur)
		if err != nil {
			return err
		}
		if next == nil {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, next, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if d.Name() == ".git" && rel != "." {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p) //nolint:gosec // G122: copies a revision snapshot from the runtime data dir, not a tree shared with an untrusted writer
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644) //nolint:gosec // G703: dst/rel come from walking src, under a destination the code chose
	})
}

// pruneEmpty drops empty strings, nils and empty arrays/objects (blank form fields).
func pruneEmpty(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch tv := v.(type) {
		case nil:
			continue
		case string:
			if tv == "" {
				continue
			}
		case []any:
			// Blank rows of a list field (an added but empty picker) are dropped too.
			items := make([]any, 0, len(tv))
			for _, it := range tv {
				if it == nil || it == "" {
					continue
				}
				items = append(items, it)
			}
			if len(items) == 0 {
				continue
			}
			v = items
		case map[string]any:
			sub := pruneEmpty(tv)
			if len(sub) == 0 {
				continue
			}
			v = sub
		}
		out[k] = v
	}
	return out
}
