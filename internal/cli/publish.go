package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/writeback"
)

// `cicord plugin publish` (ADR-0011): run by a plugin author in the plugin's repository. It
// validates the plugin like `cicord plugin build`, checks that plugin.yaml's version is a
// new release, tags it v<version> (pushed with --push) and produces the registry index
// entry, written to a local registry checkout with --registry.
//
// It uses the git command line (the author's credentials, ssh and signing configuration).

func init() {
	pluginSubcommands = append(pluginSubcommands, newPluginPublishCmd)
}

type publishOptions struct {
	push, dryRun, replaceRepo bool
	remote, registryRef       string
	repoURL, publisher        string
}

func newPluginPublishCmd() *cobra.Command {
	var o publishOptions
	cmd := &cobra.Command{
		Use:   "publish [plugin-repo]",
		Short: "Release a plugin: validate, tag v<version>, write its registry index entry",
		Long: `Release the plugin whose repository is [plugin-repo] (default: the current directory;
plugin.yaml at the root of a git repository):

  1. validate it like ` + "`cicord plugin build`" + ` (bundle, inspect, manifest checks);
  2. check the working tree is clean (the tag must point at what was validated) and
     that metadata.version is a semver greater than every existing v* tag;
  3. create the annotated tag v<version> on HEAD (--push also pushes it to --remote);
  4. print the registry index entry, or with --registry <dir> write it to the
     index.yaml of a local registry checkout (then commit there and open a pull request).

The repo URL of the entry comes from --repo or the --remote URL (ssh URLs become https).
URLs with credentials are refused. publish never sets "verified": registry maintainers
do. A registry whose index is signed (index.yaml.sig) must be signed again by its
maintainers after the change (` + "`cicord registry sign`" + `).

--dry-run validates and prints what would be done, changing nothing.`,
		Example: `  cicord plugin publish --dry-run
  cicord plugin publish --push --registry ../cicord-registry --publisher acme
  cicord plugin publish ./my-plugin --repo https://github.com/acme/cicord-xp.git`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runPublish(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.push, "push", false, "push the tag to --remote")
	f.BoolVar(&o.dryRun, "dry-run", false, "validate and print what would be done, change nothing")
	f.StringVar(&o.remote, "remote", "origin", "git remote to push to and to read the repo URL from")
	f.StringVar(&o.registryRef, "registry", "", "local checkout of a registry to update (an https URL only prints the steps)")
	f.StringVar(&o.repoURL, "repo", "", "repository URL for the index entry (default: the --remote URL)")
	f.StringVar(&o.publisher, "publisher", "", "publisher name for the index entry (default: the existing entry's)")
	f.BoolVar(&o.replaceRepo, "replace-repo", false, "allow changing the repo of an existing index entry")
	return cmd
}

// gitCmd runs git in dir and returns its trimmed stdout.
func gitCmd(ctx context.Context, dir string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G204: runs git with arguments built by the publish command
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), registry.Redact(msg))
	}
	return strings.TrimSpace(out.String()), nil
}

var scpLikeRe = regexp.MustCompile(`^([A-Za-z0-9._-]+@)?([A-Za-z0-9.-]+):([^/].*)$`)

// publicRepoURL turns a git remote URL into the URL to publish: ssh forms become https,
// credentials are refused (remote URLs often embed tokens).
func publicRepoURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "ssh://"):
		rest := strings.TrimPrefix(s, "ssh://")
		host, p, _ := strings.Cut(rest, "/")
		if _, h, ok := strings.Cut(host, "@"); ok {
			host = h
		}
		host, _, _ = strings.Cut(host, ":") // ssh port
		s = "https://" + host + "/" + p
	case !strings.Contains(s, "://") && scpLikeRe.MatchString(s):
		m := scpLikeRe.FindStringSubmatch(s)
		s = "https://" + m[2] + "/" + m[3]
	}
	if registry.HasUserinfo(s) {
		return "", errors.New("the repository URL contains credentials: pass a clean URL with --repo")
	}
	return s, nil
}

// publishedVersions lists the semver tags of a repository (local tags, plus the remote's
// with remote != "").
func publishedVersions(ctx context.Context, dir, remote string) ([]*semver.Version, map[string]bool, error) {
	out, err := gitCmd(ctx, dir, "tag", "--list")
	if err != nil {
		return nil, nil, err
	}
	names := strings.Fields(out)
	if remote != "" {
		rout, err := gitCmd(ctx, dir, "ls-remote", "--tags", remote)
		if err != nil {
			return nil, nil, err
		}
		for _, line := range strings.Split(rout, "\n") {
			f := strings.Fields(line)
			if len(f) == 2 {
				names = append(names, strings.TrimSuffix(strings.TrimPrefix(f[1], "refs/tags/"), "^{}"))
			}
		}
	}
	tags := map[string]bool{}
	var vs []*semver.Version
	for _, n := range names {
		tags[n] = true
		if v, err := semver.NewVersion(n); err == nil {
			vs = append(vs, v)
		}
	}
	return vs, tags, nil
}

func runPublish(ctx context.Context, w, errw io.Writer, dir string, o publishOptions) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	top, err := gitCmd(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%s is not in a git repository: %w", dir, err)
	}
	if a, b := evalPath(abs), evalPath(top); a != b {
		return fmt.Errorf("plugin.yaml must be at the root of its git repository (%s), not in %s", top, abs)
	}

	// 1. Manifest and build.
	m, err := readManifest(abs)
	if err != nil {
		return err
	}
	name := m.Metadata.Name
	if !registry.NameRe.MatchString(name) {
		return fmt.Errorf("plugin.yaml: metadata.name %q must use lowercase letters, digits and dashes (it is the registry key)", name)
	}
	ver, err := semver.StrictNewVersion(m.Metadata.Version)
	if err != nil {
		return fmt.Errorf("plugin.yaml: metadata.version %q is not a semver version like 1.2.0", m.Metadata.Version)
	}
	r, err := buildPluginReport(abs)
	if err != nil {
		return err
	}
	if len(r.Errors) > 0 {
		printPluginReport(w, r)
		return errors.New("the plugin does not build: fix the errors above before publishing")
	}
	fmt.Fprintf(w, "✓ %s %s builds (%d command(s), %.1f KiB bundle)\n", name, ver, len(r.Commands), float64(r.BundleBytes)/1024)
	for _, x := range r.Warnings {
		fmt.Fprintf(errw, "warning: %s\n", x)
	}

	// 2. Clean tree, new version.
	status, err := gitCmd(ctx, abs, "status", "--porcelain")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("the working tree has uncommitted changes: commit them first, so that the tag points at what was validated\n%s", status)
	}
	remoteForTags := ""
	if o.push {
		remoteForTags = o.remote
	}
	versions, tags, err := publishedVersions(ctx, abs, remoteForTags)
	if err != nil {
		return err
	}
	tag := "v" + ver.String()
	if tags[tag] {
		return fmt.Errorf("tag %s already exists: bump metadata.version in plugin.yaml", tag)
	}
	for _, v := range versions {
		if !ver.GreaterThan(v) {
			return fmt.Errorf("version %s is not greater than the existing tag %s: bump metadata.version in plugin.yaml", ver, v.Original())
		}
	}
	where := "local tags"
	if o.push {
		where = "local and " + o.remote + " tags"
	}
	fmt.Fprintf(w, "✓ %s is a new version (%d earlier release(s) in %s)\n", ver, len(versions), where)
	commit, err := gitCmd(ctx, abs, "rev-parse", "HEAD")
	if err != nil {
		return err
	}

	// 3. The index entry (computed and checked before anything is written).
	repoURL := o.repoURL
	if repoURL == "" {
		if repoURL, err = gitCmd(ctx, abs, "remote", "get-url", o.remote); err != nil {
			return fmt.Errorf("no repository URL: add a %q remote or pass --repo (%w)", o.remote, err)
		}
	}
	if repoURL, err = publicRepoURL(repoURL); err != nil {
		return err
	}
	if !registry.IsRemoteURL(repoURL) {
		fmt.Fprintf(errw, "warning: %s is not an https:// URL: https registries (and the admin UI) cannot install from it\n", repoURL)
	}
	entry := registry.IndexEntry{Repo: repoURL, DisplayName: m.Metadata.DisplayName, Description: m.Metadata.Description,
		Keywords: m.Metadata.Keywords, Publisher: o.publisher, Version: ver.String(), Commit: commit}

	var plan *indexPlan
	localRegistry := o.registryRef != "" && !strings.Contains(o.registryRef, "://")
	if localRegistry {
		if plan, err = planIndexUpdate(o.registryRef, name, entry, o.replaceRepo); err != nil {
			return err
		}
		entry = plan.entry
	}

	// 4. Tag (and push).
	if o.dryRun {
		fmt.Fprintf(w, "• would tag %s at %s", tag, shortCommit(commit))
		if o.push {
			fmt.Fprintf(w, " and push it to %s", o.remote)
		}
		fmt.Fprintln(w)
	} else {
		if _, err := gitCmd(ctx, abs, "tag", "-a", tag, "-m", fmt.Sprintf("%s %s", name, ver), commit); err != nil {
			return err
		}
		fmt.Fprintf(w, "✓ tagged %s at %s\n", tag, shortCommit(commit))
		if o.push {
			if _, err := gitCmd(ctx, abs, "push", o.remote, "refs/tags/"+tag); err != nil {
				return fmt.Errorf("%w\nthe tag exists locally: retry with `git push %s %s`", err, o.remote, tag)
			}
			fmt.Fprintf(w, "✓ pushed %s to %s\n", tag, o.remote)
		} else {
			fmt.Fprintf(w, "  push it when ready: git push %s %s (or rerun with --push next time)\n", o.remote, tag)
		}
	}

	// 5. Registry.
	var snippet bytes.Buffer
	enc := yaml.NewEncoder(&snippet)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]registry.IndexEntry{name: entry}); err != nil {
		return err
	}
	switch {
	case plan != nil:
		if o.dryRun {
			fmt.Fprintf(w, "• would %s %s in %s:\n", plan.verb, name, plan.file)
			writeIndented(w, snippet.Bytes())
			break
		}
		if err := os.WriteFile(plan.file, plan.content, 0o644); err != nil { //nolint:gosec // G703: index file of the registry checkout the user passed with --registry
			return err
		}
		fmt.Fprintf(w, "✓ %s %s in %s\n", plan.verbPast, name, plan.file)
		if plan.signed {
			fmt.Fprintf(w, "! %s is now stale: the registry maintainers must sign the index again\n  (cicord registry sign %s --key …)\n",
				filepath.Join(o.registryRef, registry.SignatureFile), o.registryRef)
		}
		fmt.Fprintf(w, "Next: commit index.yaml in %s and open a pull request to the registry.\n", o.registryRef)
	default:
		fmt.Fprintln(w, "Registry index entry (plugins: in the registry's index.yaml):")
		writeIndented(w, snippet.Bytes())
		if o.registryRef != "" {
			fmt.Fprintf(w, "To publish it in %s: clone the registry, run\n  cicord plugin publish --registry <checkout> (or edit index.yaml with the entry above),\nthen commit and open a pull request.\n", registry.Redact(o.registryRef))
		}
	}
	return nil
}

func shortCommit(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func evalPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

func writeIndented(w io.Writer, b []byte) {
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// indexPlan is the change to a local registry checkout's index.yaml.
type indexPlan struct {
	file           string
	content        []byte
	entry          registry.IndexEntry
	verb, verbPast string
	signed         bool
}

// planIndexUpdate computes the new index.yaml of a registry checkout with entry for name.
// An existing entry keeps its fields that publish does not set (verified, publisher,
// unknown keys); its repo only changes with replaceRepo.
func planIndexUpdate(dir, name string, entry registry.IndexEntry, replaceRepo bool) (*indexPlan, error) {
	file := filepath.Join(dir, registry.IndexFile)
	cur, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s has no %s: --registry is a local checkout of a registry repository", dir, registry.IndexFile)
	} else if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(string(cur)) {
	case "", "plugins:", "plugins: {}":
		cur = []byte("plugins:\n") // a block mapping, not {xp: {…}}
	}
	idx, err := registry.ParseIndex(cur)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	var raw struct {
		Plugins map[string]map[string]any `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(cur, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	p := &indexPlan{file: file, verb: "add", verbPast: "added"}
	value := map[string]any{}
	if old, ok := idx.Plugins[name]; ok {
		p.verb, p.verbPast = "update", "updated"
		if config.NormalizeRegistryURL(old.Repo) != config.NormalizeRegistryURL(entry.Repo) && !replaceRepo {
			return nil, fmt.Errorf("%s already lists %s with the repository %s (not %s): use --replace-repo if it moved",
				file, name, registry.Redact(old.Repo), entry.Repo)
		}
		for k, v := range raw.Plugins[name] {
			value[k] = v // keep keys publish does not manage
		}
		entry.Verified = old.Verified // never set by the publisher
		if entry.Publisher == "" {
			entry.Publisher = old.Publisher
		}
	}
	set := func(k, v string) {
		if v != "" {
			value[k] = v
		} else {
			delete(value, k)
		}
	}
	set("repo", entry.Repo)
	set("displayName", entry.DisplayName)
	set("description", entry.Description)
	set("publisher", entry.Publisher)
	set("version", entry.Version)
	set("commit", entry.Commit)
	if len(entry.Keywords) > 0 {
		kw := make([]any, len(entry.Keywords))
		for i, k := range entry.Keywords {
			kw[i] = k
		}
		value["keywords"] = kw
	} else {
		delete(value, "keywords")
	}
	next, err := writeback.SetYAML(cur, []writeback.PathElem{writeback.Key("plugins"), writeback.Key(name)}, value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if _, err := registry.ParseIndex(next); err != nil {
		return nil, fmt.Errorf("%s would become invalid: %w", file, err)
	}
	p.content, p.entry = next, entry
	if _, err := os.Stat(filepath.Join(dir, registry.SignatureFile)); err == nil {
		p.signed = true
	}
	return p, nil
}
