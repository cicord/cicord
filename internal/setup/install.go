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
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/writeback"
)

// Installer installs catalog plugins into the active repository through write-back:
// the plugin sources are copied to plugins/<name>/ and an instance (with every
// capability the plugin declares granted) is appended to the bot's spec.plugins.
type Installer struct {
	Runtime *runtime.Runtime
	Writer  writeback.Writer
	Catalog *catalog.Catalog
	Probe   Probe
	Secrets func(string) (string, bool)
	// Timeout bounds the wait for the new revision (default 30s).
	Timeout time.Duration
}

// Install installs req.Plugin for bot botKey on serverKey.
func (in *Installer) Install(ctx context.Context, serverKey, botKey string, req InstallRequest, source, actor string) (InstallResult, error) {
	rt := in.Runtime
	repo := rt.Repo()
	rev, ok := rt.Revision()
	if repo == nil || !ok {
		return InstallResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "No active configuration yet.")
	}
	if in.Writer == nil {
		return InstallResult{}, errorf(http.StatusServiceUnavailable, "unavailable", "", "Write-back is not configured: plugins cannot be installed from the admin.")
	}
	entry, ok := in.Catalog.Get(req.Plugin)
	if !ok {
		return InstallResult{}, errorf(http.StatusNotFound, "not_found", "", "Unknown plugin %q.", req.Plugin)
	}
	bot := repo.Bots[botKey]
	if bot == nil || !slices.Contains(bot.Spec.Servers, serverKey) {
		return InstallResult{}, errorf(http.StatusNotFound, "not_found", "", "Bot %q does not manage server %q.", botKey, serverKey)
	}
	if slices.ContainsFunc(bot.Spec.Plugins, func(p config.PluginInstance) bool { return p.Name == entry.Name }) {
		return InstallResult{}, errorf(http.StatusConflict, "conflict", "Open Plugins in the admin to configure it.",
			"%s is already installed.", entry.DisplayName)
	}

	// Privileged intents: a plugin needing an intent the application has not enabled
	// would make Discord refuse the bot's gateway connection.
	if len(entry.PrivilegedIntents) > 0 && in.Probe != nil {
		tok, has := in.Secrets(bot.Spec.Token.SecretRef)
		if has {
			id, err := in.Probe.Identify(ctx, tok)
			if err != nil {
				return InstallResult{}, explainProbeErr(err)
			}
			need, _ := discord.ParseIntents(entry.PrivilegedIntents)
			if c := IntentsCheck(bot.Spec.ApplicationID, id.Flags, need, 0); c.Status == StatusFail {
				return InstallResult{}, &Error{Status: http.StatusConflict, Code: "intents",
					Message: fmt.Sprintf("%s needs a privileged intent that is turned off. %s", entry.DisplayName, c.Message),
					Hint:    c.Hint, Link: c.Link}
			}
		}
	}

	files, err := in.Catalog.Files(entry.Name)
	if err != nil {
		return InstallResult{}, err
	}
	botDir := path.Dir(bot.File)
	rel, _ := filepath.Rel(filepath.FromSlash(botDir), filepath.FromSlash(catalog.PluginDir(entry.Name)))
	pi := config.PluginInstance{Name: entry.Name, Source: filepath.ToSlash(rel), Grants: slices.Clone(entry.Capabilities)}
	if len(bot.Spec.Servers) > 1 {
		pi.Servers = []string{serverKey}
	}
	if cfg := pruneEmpty(req.Config); len(cfg) > 0 {
		pi.Config = cfg
	}
	edits := map[string]writeback.EditFunc{}
	for p, data := range files {
		data := data
		p := p
		edits[p] = func(cur []byte) ([]byte, error) {
			if cur != nil && !bytes.Equal(cur, data) {
				return nil, fmt.Errorf("%s already exists with other content", p)
			}
			return data, nil
		}
	}
	botEdit := func(cur []byte) ([]byte, error) {
		if cur == nil {
			return nil, errors.New("bot file does not exist")
		}
		return writeback.AppendYAML(cur, []writeback.PathElem{writeback.Key("spec"), writeback.Key("plugins")}, pi)
	}
	edits[bot.File] = botEdit

	// Validate (schema, references, build, intents) on a copy of the active revision.
	if ds, err := in.validate(rev.Dir, files, bot.File, botEdit); err != nil {
		return InstallResult{}, err
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
		Message: fmt.Sprintf("plugin(%s): install %s %s on %s\n\nInstalled from the %s catalog.", botKey, entry.Name, entry.Version, serverKey, entry.Source),
		Actor:   actor, Source: source, Mode: mode})
	if err != nil {
		if errors.Is(err, writeback.ErrConflict) {
			return InstallResult{}, &Error{Status: http.StatusConflict, Code: "conflict", Message: "The repository changed meanwhile.", Hint: "Try again.", Details: err.Error()}
		}
		return InstallResult{}, &Error{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "Could not save the plugin to the repository.",
			Hint: "Check the repository access (Diagnostics → write-back), then try again.", Details: err.Error()}
	}
	out := InstallResult{Instance: entry.Name, Mode: res.Mode, PullRequestURL: res.PullRequestURL, Health: runtime.HealthProgressing}
	if out.Mode == "" {
		out.Mode = mode
	}
	if len(entry.Commands) > 0 {
		out.Command = entry.Commands[0].Name
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
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		if cur, ok := rt.Revision(); ok && cur.SHA != rev.SHA {
			out.Revision = cur.ID
			for _, i := range rt.Instances(serverKey) {
				if i.Config.Bot == botKey && i.Config.Name == entry.Name {
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
		case <-t.C:
		}
	}
}

func (in *Installer) validate(revDir string, files map[string][]byte, botFile string, botEdit writeback.EditFunc) (config.Diagnostics, error) {
	tmp, err := os.MkdirTemp("", "codi-install-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := copyDir(revDir, tmp); err != nil {
		return nil, err
	}
	if err := writeTree(tmp, files); err != nil {
		return nil, err
	}
	bp := filepath.Join(tmp, filepath.FromSlash(botFile))
	cur, err := os.ReadFile(bp)
	if err != nil {
		return nil, err
	}
	next, err := botEdit(cur)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(bp, next, 0o644); err != nil {
		return nil, err
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
