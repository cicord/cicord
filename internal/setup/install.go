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
	"sort"
	"strings"
	"time"

	"github.com/npjonath/codi/internal/catalog"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/writeback"
)

// Installer installs plugins into the active repository through write-back, from the
// bundled catalog (sources copied to plugins/<name>/) or from a registry (codi.lock entry
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
	// From names where it comes from, for the commit message ("the codi catalog").
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
	if bot == nil || !slices.Contains(bot.Spec.Servers, t.Server) {
		return InstallResult{}, errorf(http.StatusNotFound, "not_found", "", "Bot %q does not manage server %q.", t.Bot, t.Server)
	}
	if slices.ContainsFunc(bot.Spec.Plugins, func(p config.PluginInstance) bool { return p.Name == instance }) {
		return InstallResult{}, errorf(http.StatusConflict, "conflict", "Open Plugins in the admin to configure it, or choose another instance name.",
			"%s is already installed as %q.", pkg.DisplayName, instance)
	}

	// Privileged intents: a plugin needing an intent the application has not enabled
	// would make Discord refuse the bot's gateway connection.
	if len(pkg.PrivilegedIntents) > 0 && in.Probe != nil && in.Secrets != nil {
		if tok, has := in.Secrets(bot.Spec.Token.SecretRef); has {
			id, err := in.Probe.Identify(ctx, tok)
			if err != nil {
				return InstallResult{}, explainProbeErr(err)
			}
			need, _ := discord.ParseIntents(pkg.PrivilegedIntents)
			if c := IntentsCheck(bot.Spec.ApplicationID, id.Flags, need, 0); c.Status == StatusFail {
				return InstallResult{}, &Error{Status: http.StatusConflict, Code: "intents",
					Message: fmt.Sprintf("%s needs a privileged intent that is turned off. %s", pkg.DisplayName, c.Message),
					Hint:    c.Hint, Link: c.Link}
			}
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
					return nil, fmt.Errorf("%s is already locked at %s: update it with codi plugin update", pkg.Source, old.Version)
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
	if len(bot.Spec.Servers) > 1 {
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
	if ds, err := in.validate(rev.Dir, edits); err != nil {
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

	paths := make([]string, 0, len(edits))
	for p := range edits {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	mode := writeback.ModeFor(repo.Project.Spec.WriteBack, paths)
	prevErr := rt.Status().LastError
	res, err := in.Writer.Write(ctx, writeback.Request{Edits: edits,
		Message: fmt.Sprintf("plugin(%s): install %s %s on %s\n\nInstalled from %s.", t.Bot, pkg.Name, pkg.Version, t.Server, pkg.From),
		Actor:   actor, Source: source, Mode: mode})
	if err != nil {
		if errors.Is(err, writeback.ErrConflict) {
			return InstallResult{}, &Error{Status: http.StatusConflict, Code: "conflict", Message: "The repository changed meanwhile.", Hint: "Try again.", Details: err.Error()}
		}
		return InstallResult{}, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Could not save the plugin to the repository.",
			Hint: "Check the repository access (Diagnostics → write-back), then try again.", Details: err.Error()}
	}
	out := InstallResult{Instance: instance, Mode: res.Mode, PullRequestURL: res.PullRequestURL, Health: runtime.HealthProgressing, Command: pkg.FirstCommand}
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

func (in *Installer) validate(revDir string, edits map[string]writeback.EditFunc) (config.Diagnostics, error) {
	tmp, err := os.MkdirTemp("", "codi-install-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := copyDir(revDir, tmp); err != nil {
		return nil, err
	}
	for rel, edit := range edits {
		p := filepath.Join(tmp, filepath.FromSlash(rel))
		cur, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			cur = nil
		} else if err != nil {
			return nil, err
		}
		next, err := edit(cur)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, next, 0o644); err != nil {
			return nil, err
		}
	}
	_, _, ds, _ := in.Runtime.LoadAndBuild(tmp)
	return ds, nil
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
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
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
			if len(tv) == 0 {
				continue
			}
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
