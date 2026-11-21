package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/reconcile"
	"github.com/npjonath/codi/internal/state"
	"github.com/npjonath/codi/internal/state/sqlite"
)

// localFlags are shared by the commands that talk to Discord directly (plan, apply,
// adopt, mv, import).
type localFlags struct {
	statePath string
	servers   []string
}

func (f *localFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.statePath, "state", "", "local state database (resource map) (default <dir>/.codi/state.db)")
}

func (f *localFlags) registerServers(cmd *cobra.Command) {
	cmd.Flags().StringSliceVar(&f.servers, "server", nil, "only these server keys (repeatable)")
}

func (f *localFlags) path(dir string) string {
	if f.statePath != "" {
		return f.statePath
	}
	return filepath.Join(dir, ".codi", "state.db")
}

// openState opens the local state database. With readOnly and no database file, an
// empty in-memory store is used (nothing is created on disk).
func (f *localFlags) openState(dir string, readOnly bool) (*sqlite.Store, string, error) {
	p := f.path(dir)
	if readOnly {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			s, err := sqlite.Open(":memory:")
			return s, "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, "", err
	}
	s, err := sqlite.Open(p)
	if err != nil {
		return nil, "", fmt.Errorf("open state database %s: %w", p, err)
	}
	return s, p, nil
}

func (f *localFlags) wants(server string) bool {
	if len(f.servers) == 0 {
		return true
	}
	for _, s := range f.servers {
		if s == server {
			return true
		}
	}
	return false
}

// loadValidRepo loads dir and prints diagnostics; it fails on errors.
func loadValidRepo(stderr io.Writer, dir string) (*config.Repo, error) {
	repo, ds := loadRepo(dir)
	printDiagnostics(stderr, ds)
	if repo == nil || ds.HasErrors() {
		return nil, exitError{1}
	}
	return repo, nil
}

// readOnlyResources makes plan-only reconcilers leave the resource map untouched.
type readOnlyResources struct{ state.ResourceMap }

func (readOnlyResources) Put(context.Context, state.Resource) error { return nil }
func (readOnlyResources) Delete(context.Context, string, state.ResourceKind, string) error {
	return nil
}

// bots connects bot sessions on demand (REST only, no gateway).
type bots struct {
	repo      *config.Repo
	connector discord.Connector
	sessions  map[string]discord.Session
	errs      map[string]error
}

func newBots(repo *config.Repo) *bots {
	return &bots{repo: repo, connector: newConnector(), sessions: map[string]discord.Session{}, errs: map[string]error{}}
}

func (b *bots) session(ctx context.Context, key string) (discord.Session, error) {
	if s, ok := b.sessions[key]; ok {
		return s, nil
	}
	if err, ok := b.errs[key]; ok {
		return nil, err
	}
	s, err := b.connect(ctx, key)
	if err != nil {
		b.errs[key] = err
		return nil, err
	}
	b.sessions[key] = s
	return s, nil
}

func (b *bots) connect(ctx context.Context, key string) (discord.Session, error) {
	bot := b.repo.Bots[key]
	ref := bot.Spec.Token.SecretRef
	token, ok := lookupSecret(ref)
	if !ok || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("bot %s: the environment variable %s (token.secretRef) is not set", key, ref)
	}
	s, err := b.connector.Connect(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("bot %s: %w", key, err)
	}
	if s.ApplicationID() != bot.Spec.ApplicationID {
		_ = s.Close(ctx)
		return nil, fmt.Errorf("bot %s: the token in %s belongs to application %s, bot.yaml says %s", key, ref, s.ApplicationID(), bot.Spec.ApplicationID)
	}
	return s, nil
}

// forServer returns the reconciling bot of a server: the first bot (by key) listing it
// whose token is available, like the runtime.
func (b *bots) forServer(ctx context.Context, server string) (string, discord.Session, error) {
	keys := make([]string, 0, len(b.repo.Bots))
	for k := range b.repo.Bots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var errs []error
	for _, k := range keys {
		for _, s := range b.repo.Bots[k].Spec.Servers {
			if s != server {
				continue
			}
			sess, err := b.session(ctx, k)
			if err == nil {
				return k, sess, nil
			}
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return "", nil, fmt.Errorf("no bot lists server %s in spec.servers", server)
	}
	return "", nil, errors.Join(errs...)
}

func (b *bots) close(ctx context.Context) {
	for _, s := range b.sessions {
		_ = s.Close(ctx)
	}
}

// serverReconciler is a reconciler for one server of a local command.
type serverReconciler struct {
	key string
	bot string
	rec *reconcile.Reconciler
	err error // no bot / cannot access the server
}

// reconcilers builds one reconciler per selected server. readOnly wraps the resource
// map so that planning never writes it.
func (f *localFlags) reconcilers(ctx context.Context, repo *config.Repo, b *bots, store state.Store, readOnly bool) []serverReconciler {
	keys := make([]string, 0, len(repo.Servers))
	for k := range repo.Servers {
		if f.wants(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []serverReconciler
	for _, key := range keys {
		srv := repo.Servers[key]
		bk, sess, err := b.forServer(ctx, key)
		if err != nil {
			out = append(out, serverReconciler{key: key, err: err})
			continue
		}
		var res state.ResourceMap = store.Resources()
		if readOnly {
			res = readOnlyResources{res}
		}
		rec := reconcile.New(reconcile.Options{
			Server: srv, RepoRoot: repo.Root, Client: sess, Resources: res,
			KV: store.KV("reconcile:" + key), BotUserID: sess.Self().ID, Reason: "codi apply (cli)",
		})
		out = append(out, serverReconciler{key: key, bot: bk, rec: rec})
	}
	return out
}

func (f *localFlags) checkServers(repo *config.Repo) error {
	for _, s := range f.servers {
		if _, ok := repo.Servers[s]; !ok {
			return fmt.Errorf("unknown server %q", s)
		}
	}
	return nil
}

func absDir(args []string) (string, error) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	return filepath.Abs(dir)
}
