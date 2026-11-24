package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/importer"
	"github.com/cicord/cicord/internal/planfmt"
	"github.com/cicord/cicord/internal/reconcile"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/state/sqlite"
)

func init() {
	extraCommands = append(extraCommands, newImportCmd, newAdoptCmd)
}

// newDownloader returns the CDN downloader of cicord import (tests: the simulator's).
var newDownloader = func() importer.Downloader { return importer.HTTPDownloader(nil) }

func newImportCmd() *cobra.Command {
	var lf localFlags
	var tokenEnv, key string
	var force bool
	cmd := &cobra.Command{
		Use:   "import <server-id> [dir]",
		Short: "Generate servers/<key>.yaml from an existing Discord server",
		Long: `Connect to Discord with a bot token, read the live server (roles with their hierarchy and
permissions, @everyone, categories, channels with their overwrites, positions, topics and
slowmode, the profile, AutoMod, onboarding, widget, welcome screen) and write
servers/<key>.yaml. The server icon, banner and invite splash, role icons, emojis,
stickers and soundboard sounds are downloaded from the Discord CDN into
servers/assets/<key>/ and referenced from the YAML.

Keys are derived from the display names (slugified, deduplicated). Roles managed by an
integration (bot roles) are skipped. The resource map (key → Discord ID) is written to
the local state database (--state, default .cicord/state.db), so that a later
` + "`cicord apply`" + ` adopts the existing resources instead of recreating them.

The bot must be a member of the server. Add the server key to a bot's spec.servers
afterwards.`,
		Example: `  DISCORD_TOKEN=… cicord import 112233445566778899
  cicord import 112233445566778899 ./infra --key ma-commu --token-env DISCORD_TOKEN_MODERATOR`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := absDir(args[1:])
			if err != nil {
				return err
			}
			return runImport(cmd, args[0], dir, key, tokenEnv, force, &lf)
		},
	}
	lf.register(cmd)
	cmd.Flags().StringVar(&tokenEnv, "token-env", "DISCORD_TOKEN", "environment variable holding the bot token")
	cmd.Flags().StringVar(&key, "key", "", "server key (default: derived from the server name)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing servers/<key>.yaml and its resource map")
	return cmd
}

func runImport(cmd *cobra.Command, serverID, dir, key, tokenEnv string, force bool, lf *localFlags) error {
	ctx := cmd.Context()
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if _, err := readProject(dir); err != nil {
		return err
	}
	if key != "" && !importer.KeyPattern(key) {
		return fmt.Errorf("--key %q is not a valid key (^[a-z][a-z0-9-]{0,62}$)", key)
	}
	// The server must not already be declared (unless it is regenerated with --force).
	if repo, _ := config.Load(dir, config.LoadOptions{}); repo != nil {
		for k, s := range repo.Servers {
			if s.Spec.DiscordID != serverID {
				continue
			}
			if !force {
				return fmt.Errorf("server %s is already declared in %s (use --force to regenerate it)", serverID, s.File)
			}
			if key != "" && key != k {
				return fmt.Errorf("server %s is already declared as %q: use --key %s or remove %s", serverID, k, k, s.File)
			}
			key = k
		}
	}
	token, ok := lookupSecret(tokenEnv)
	if !ok || strings.TrimSpace(token) == "" {
		return fmt.Errorf("the environment variable %s (bot token) is not set", tokenEnv)
	}
	sess, err := newConnector().Connect(ctx, token)
	if err != nil {
		return err
	}
	defer sess.Close(context.WithoutCancel(ctx))
	// With the bot member, overwrites of the bot's own managed role import as "@bot".
	snap, err := reconcile.FetchWith(ctx, sess, serverID, sess.Self().ID, reconcile.FetchAll)
	if err != nil {
		// AutoMod, onboarding, widget… need Manage Server: import the rest
		full := err
		if snap, err = reconcile.Fetch(ctx, sess, serverID, sess.Self().ID); err != nil {
			return fmt.Errorf("read server %s (is the bot a member?): %w", serverID, err)
		}
		fmt.Fprintf(stderr, "warning: AutoMod rules, onboarding, welcome screen, widget and expressions not read: %v\n", full)
	}
	res, err := importer.Generate(ctx, importer.Options{ServerKey: key, Snapshot: snap, Download: newDownloader()})
	if err != nil {
		return err
	}
	file := filepath.Join(dir, "servers", res.Key+".yaml")
	if _, err := os.Stat(file); err == nil && !force {
		return fmt.Errorf("%s already exists (use --key to choose another key, or --force)", file)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, res.YAML, 0o644); err != nil {
		return err
	}
	for _, f := range res.Files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, f.Data, 0o644); err != nil {
			return err
		}
	}
	store, statePath, err := lf.openState(dir, false)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := replaceGitMappings(ctx, store, res.Key, res.Resources); err != nil {
		return err
	}
	if err := res.RecordFiles(ctx, store.KV("reconcile:"+res.Key)); err != nil {
		return err
	}

	rel, _ := filepath.Rel(dir, file)
	counts := map[state.ResourceKind]int{}
	for _, r := range res.Resources {
		counts[r.Kind]++
	}
	fmt.Fprintf(stdout, "Imported %q as %s: %d role(s), %d categor(ies), %d channel(s)\n",
		snap.Server.Name, rel, counts[state.KindRole], counts[state.KindCategory], counts[state.KindChannel])
	if n := len(res.Files); n > 0 {
		fmt.Fprintf(stdout, "Downloaded %d file(s) (icons, emojis, stickers, sounds) into %s\n", n, filepath.Join("servers", "assets", res.Key))
	}
	fmt.Fprintf(stdout, "Resource map written to %s\n", statePath)
	for _, w := range res.Warnings {
		fmt.Fprintln(stderr, "warning:", w)
	}

	// The generated file must validate.
	repo, ds := loadRepo(dir)
	var own config.Diagnostics
	for _, d := range ds {
		if d.File == filepath.ToSlash(rel) {
			own = append(own, d)
		}
	}
	printDiagnostics(stderr, own)
	if own.HasErrors() {
		return fmt.Errorf("the generated %s does not validate (please report this bug)", rel)
	}
	listed := false
	if repo != nil {
		for _, b := range repo.Bots {
			for _, s := range b.Spec.Servers {
				if s == res.Key {
					listed = true
				}
			}
		}
	}
	if !listed {
		fmt.Fprintf(stdout, "\nNext: add %q to the spec.servers of the bot that manages it, then run `cicord plan`.\n", res.Key)
	} else {
		fmt.Fprintln(stdout, "\nNext: run `cicord plan` (it should show no changes).")
	}
	return nil
}

// replaceGitMappings replaces the git-owned resource map entries of a server.
func replaceGitMappings(ctx context.Context, store state.Store, server string, rs []state.Resource) error {
	cur, err := store.Resources().List(ctx, server)
	if err != nil {
		return err
	}
	for _, r := range cur {
		if r.Owner == state.OwnerGit {
			if err := store.Resources().Delete(ctx, server, r.Kind, r.Key); err != nil {
				return err
			}
		}
	}
	for _, r := range rs {
		r.Server = server
		r.Owner = state.OwnerGit
		if err := store.Resources().Put(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// ───────────────────────────────────────────────────────────── adopt

func newAdoptCmd() *cobra.Command {
	var lf localFlags
	var yes, reset bool
	cmd := &cobra.Command{
		Use:   "adopt [dir]",
		Short: "Rebuild the local resource map by matching display names",
		Long: `Rebuild the resource map (key → Discord ID) of the local state database after it was
lost or became stale, by matching each declared role, category and channel with an
existing Discord resource of the same kind (same type for channels) and the same display
name. Mappings that still point to an existing resource are kept, mappings to deleted
resources are dropped; --reset ignores every existing mapping and matches everything by
name again.

The matches are shown and must be confirmed (--yes to skip; required without a
terminal). Keys without a match are created by the next ` + "`cicord apply`" + `.
Nothing is changed in Discord.`,
		Example: `  cicord adopt
  cicord adopt --server ma-commu --reset --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := absDir(args)
			if err != nil {
				return err
			}
			return runAdopt(cmd, dir, &lf, yes, reset)
		},
	}
	lf.register(cmd)
	lf.registerServers(cmd)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "write the mappings without asking for confirmation")
	cmd.Flags().BoolVar(&reset, "reset", false, "ignore existing mappings and match every key by name")
	return cmd
}

type adoptPlan struct {
	server  string
	keep    int
	stale   []state.Resource
	adopt   []planfmt.Change
	missing []string
	keyMap  map[state.ResourceKind]map[string]bool
}

func runAdopt(cmd *cobra.Command, dir string, lf *localFlags, yes, reset bool) error {
	ctx := cmd.Context()
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	repo, err := loadValidRepo(stderr, dir)
	if err != nil {
		return err
	}
	if err := lf.checkServers(repo); err != nil {
		return err
	}
	store, statePath, err := lf.openState(dir, false)
	if err != nil {
		return err
	}
	defer store.Close()
	b := newBots(repo)
	defer b.close(context.WithoutCancel(ctx))

	keys := make([]string, 0, len(repo.Servers))
	for k := range repo.Servers {
		if lf.wants(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var plans []adoptPlan
	var errs []error
	for _, key := range keys {
		ap, err := planAdopt(ctx, repo, b, store, key, reset)
		if err != nil {
			errs = append(errs, fmt.Errorf("server %s: %w", key, err))
			continue
		}
		plans = append(plans, ap)
	}
	changes := 0
	for _, ap := range plans {
		fmt.Fprintf(stdout, "Server %s: %d mapping(s) kept, %d to adopt, %d stale\n", ap.server, ap.keep, len(ap.adopt), len(ap.stale))
		for _, c := range ap.adopt {
			fmt.Fprintf(stdout, "  = %s %s → %s\n", c.Kind, c.Key, strings.TrimPrefix(c.Summary, "adopt "))
		}
		for _, r := range ap.stale {
			fmt.Fprintf(stdout, "  - forget %s %s → %s (no longer in Discord)\n", r.Kind, r.Key, r.ID)
		}
		for _, m := range ap.missing {
			fmt.Fprintf(stdout, "  + %s: no resource with this name, created by the next apply\n", m)
		}
		changes += len(ap.adopt) + len(ap.stale)
	}
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitError{1}
	}
	if changes == 0 {
		fmt.Fprintln(stdout, "Nothing to change in the resource map.")
		return nil
	}
	if !yes {
		ok, err := confirm(cmd, "\nWrite these mappings?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(stderr, "Adopt cancelled.")
			return exitError{1}
		}
	}
	rm := store.Resources()
	for _, ap := range plans {
		for _, r := range ap.stale {
			if err := rm.Delete(ctx, ap.server, r.Kind, r.Key); err != nil {
				return err
			}
		}
		if reset {
			cur, err := rm.List(ctx, ap.server)
			if err != nil {
				return err
			}
			for _, r := range cur {
				if r.Owner == state.OwnerGit && ap.keyMap[r.Kind][r.Key] {
					if err := rm.Delete(ctx, ap.server, r.Kind, r.Key); err != nil {
						return err
					}
				}
			}
		}
		for _, c := range ap.adopt {
			if err := rm.Put(ctx, state.Resource{Server: ap.server, Kind: state.ResourceKind(c.Kind), Key: c.Key, ID: c.ID, Owner: state.OwnerGit}); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(stdout, "Resource map updated in %s\n", statePath)
	return nil
}

// planAdopt computes the adoptions of a server with the reconciler's own matching
// rules, against a scratch copy of the resource map without stale git mappings.
func planAdopt(ctx context.Context, repo *config.Repo, b *bots, store state.Store, key string, reset bool) (adoptPlan, error) {
	ap := adoptPlan{server: key, keyMap: map[state.ResourceKind]map[string]bool{}}
	srv := repo.Servers[key]
	_, sess, err := b.forServer(ctx, key)
	if err != nil {
		return ap, err
	}
	cur, err := store.Resources().List(ctx, key)
	if err != nil {
		return ap, err
	}
	snap, err := reconcile.FetchWith(ctx, sess, srv.Spec.DiscordID, sess.Self().ID, reconcile.FetchOptionsFor(&srv.Spec, cur))
	if err != nil {
		return ap, err
	}
	alive := map[string]bool{}
	for _, r := range snap.Roles {
		alive[r.ID] = true
	}
	for _, c := range snap.Channels {
		alive[c.ID] = true
	}
	for _, r := range snap.AutoMod {
		alive[r.ID] = true
	}
	for _, e := range snap.Emojis {
		alive[e.ID] = true
	}
	for _, e := range snap.Stickers {
		alive[e.ID] = true
	}
	for _, e := range snap.Sounds {
		alive[e.ID] = true
	}
	scratch, err := sqlite.Open(":memory:")
	if err != nil {
		return ap, err
	}
	defer scratch.Close()
	for _, r := range cur {
		switch {
		case r.Owner != state.OwnerGit:
		case !alive[r.ID]:
			ap.stale = append(ap.stale, r)
			continue
		case reset:
			continue
		default:
			ap.keep++
		}
		if err := scratch.Resources().Put(ctx, r); err != nil {
			return ap, err
		}
	}
	rec := reconcile.New(reconcile.Options{Server: srv, RepoRoot: repo.Root, Client: sess,
		Resources: readOnlyResources{scratch.Resources()}, KV: scratch.KV("reconcile:" + key), BotUserID: sess.Self().ID})
	defer rec.Close()
	p, err := rec.Plan(ctx)
	// Adoption only records mappings: a permission problem of the other changes is
	// reported by plan / apply, not here.
	if pe := (*reconcile.PermissionsError)(nil); err != nil && !errors.As(err, &pe) {
		return ap, err
	}
	fp, err := planfmt.FromReconcile(p)
	if err != nil {
		return ap, err
	}
	for _, c := range fp[0].Changes {
		switch c.Action {
		case reconcile.ActionAdopt:
			ap.adopt = append(ap.adopt, c)
			k := state.ResourceKind(c.Kind)
			if ap.keyMap[k] == nil {
				ap.keyMap[k] = map[string]bool{}
			}
			ap.keyMap[k][c.Key] = true
		case reconcile.ActionCreate:
			if c.Kind == reconcile.KindRole || c.Kind == reconcile.KindCategory || c.Kind == reconcile.KindChannel {
				ap.missing = append(ap.missing, c.Kind+" "+c.Key)
			}
		}
	}
	return ap, nil
}
