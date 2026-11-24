package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/api"
	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord/disgoadapter"
	"github.com/cicord/cicord/internal/gitauth"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/webui"
	"github.com/cicord/cicord/internal/writeback"
)

func init() {
	extraCommands = append(extraCommands, newServeCmd, newDevCmd)
}

// serverFlags are shared by `cicord serve` and `cicord dev`.
type serverFlags struct {
	dataDir     string
	databaseURL string // --database-url (see databaseURL for the environment fallback)
	listen      string
	publicURL   string
	adminURL    string // --admin-url (serve only)
	corsOrigins []string
	logLevel    string
	// githubAppKeyFile is the GitHub App private key file (serve only).
	githubAppKeyFile string
	// metricsListen is the address of the Prometheus metrics listener ("" disables it; serve only).
	metricsListen string
}

func (f *serverFlags) register(cmd *cobra.Command, defListen string) {
	cmd.Flags().StringVar(&f.dataDir, "data-dir", "", "runtime state directory (database, caches, keys)")
	cmd.Flags().StringVar(&f.databaseURL, "database-url", "", databaseURLFlagHelp)
	cmd.Flags().StringVar(&f.listen, "listen", defListen, "HTTP listen address")
	cmd.Flags().StringVar(&f.publicURL, "public-url", os.Getenv("CICORD_PUBLIC_URL"), "public URL of the runtime (OAuth redirects, portal links); default http://<listen>")
	cmd.Flags().StringSliceVar(&f.corsOrigins, "cors-origin", splitEnv("CICORD_CORS_ORIGINS"), "external portal origin allowed with credentials (repeatable)")
	cmd.Flags().StringVar(&f.logLevel, "log-level", "info", "log level: debug | info | warn | error")
}

func splitEnv(name string) []string {
	var out []string
	for _, s := range strings.Split(os.Getenv(name), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func newLogger(w io.Writer, level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l}))
}

// readProject decodes cicord.yaml (without full validation: the runtime validates revisions).
func readProject(dir string) (*config.Project, error) {
	data, err := os.ReadFile(filepath.Join(dir, "cicord.yaml"))
	if err != nil {
		return nil, fmt.Errorf("%s is not a Cicord repository: %w", dir, err)
	}
	var p config.Project
	if err := config.DecodeYAML(data, &p); err != nil {
		return nil, fmt.Errorf("cicord.yaml: %w", err)
	}
	return &p, nil
}

// publicURLFor returns the configured public URL or one derived from the listener.
func publicURLFor(flag string, ln net.Listener) string {
	if flag != "" {
		return strings.TrimRight(flag, "/")
	}
	addr := ln.Addr().(*net.TCPAddr)
	host := addr.IP.String()
	if addr.IP.IsUnspecified() {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, fmt.Sprint(addr.Port)))
}

// linkKey returns CICORD_LINK_SECRET or a key persisted in the data dir.
func linkKey(dataDir string) ([]byte, error) {
	if v := os.Getenv("CICORD_LINK_SECRET"); v != "" {
		return []byte(v), nil
	}
	p := filepath.Join(dataDir, "link.key")
	if data, err := os.ReadFile(p); err == nil && len(data) >= 32 {
		return data, nil
	}
	k := auth.RandomKey()
	if err := os.WriteFile(p, k, 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

// githubRepo infers "owner/repo" from a github.com remote URL.
func githubRepo(remote string) string {
	s := strings.TrimSuffix(remote, ".git")
	if rest, ok := strings.CutPrefix(s, "git@github.com:"); ok {
		return rest
	}
	u, err := url.Parse(s)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// ensureEmbeddedRepo creates the embedded bare repository (data dir) seeded with the
// contents of dir on first start.
func ensureEmbeddedRepo(bare, dir, branch string) error {
	if _, err := git.PlainOpen(bare); err == nil {
		return nil
	}
	work, err := os.MkdirTemp("", "cicord-embedded-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	r, err := git.PlainInitWithOptions(work, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName(branch)}})
	if err != nil {
		return err
	}
	if err := copyRepoTree(dir, work); err != nil {
		return err
	}
	wt, err := r.Worktree()
	if err != nil {
		return err
	}
	if err := wt.AddGlob("."); err != nil {
		return err
	}
	sig := &object.Signature{Name: "cicord[bot]", Email: "cicord[bot]@users.noreply.github.com", When: time.Now()}
	if _, err := wt.Commit("Initial import\n\nCicord-Source: cli\n", &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		return err
	}
	_, err = git.PlainClone(bare, true, &git.CloneOptions{URL: work})
	return err
}

func copyRepoTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".cicord", ".cicord-data", "dist":
				if rel != "." {
					return filepath.SkipDir
				}
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p) //nolint:gosec // G122: copies the user's own repository, not a tree shared with an untrusted writer
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644) //nolint:gosec // G703: dst/rel come from walking src, under a destination the code chose
	})
}

// checkLocalDataDir keeps the data dir out of a locally watched repository: the local
// source snapshots every file of the tree except a few ignored names (.cicord, .git…), so a
// database written inside it would trigger a new revision on every write. A data dir in
// the repo must live under .cicord/. When the user did not choose one, <repo>/.cicord/data is
// used instead.
func checkLocalDataDir(repoDir, dataDir string, explicit bool) (string, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	rel, inside := relInside(repoDir, abs)
	if !inside {
		return abs, nil // outside the repository
	}
	if first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]; first == ".cicord" {
		return abs, nil
	}
	if !explicit {
		return filepath.Join(repoDir, ".cicord", "data"), nil
	}
	return "", fmt.Errorf("--data-dir %s is inside the watched repository %s: use a path under %s or outside the repository",
		dataDir, repoDir, filepath.Join(repoDir, ".cicord"))
}

// relInside returns p relative to root, and whether p is root or below it.
func relInside(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// runHTTP serves h on ln and runs the runtime until ctx ends, then shuts both down.
// stopping is closed when the shutdown starts (see api.Options.Stopping).
func runHTTP(ctx context.Context, log *slog.Logger, ln net.Listener, h http.Handler, rt interface{ Run(context.Context) error }, stopping chan struct{}) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // plan uploads
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: SSE streams are long-lived; handlers have their own timeouts.
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	var once sync.Once
	srv.RegisterOnShutdown(func() { once.Do(func() { close(stopping) }) })
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rtDone := make(chan error, 1)
	go func() { rtDone <- rt.Run(rctx) }()
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Serve(ln) }()

	var err error
	rtStopped := false
	select {
	case <-ctx.Done():
	case err = <-srvErr:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	case err = <-rtDone:
		rtStopped = true
	}
	log.Info("shutting down")
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	_ = srv.Shutdown(sctx)
	cancel()
	if !rtStopped {
		select {
		case rerr := <-rtDone:
			if err == nil {
				err = rerr
			}
		case <-time.After(15 * time.Second):
			log.Warn("runtime did not stop in time")
		}
	}
	return err
}

func newServeCmd() *cobra.Command {
	var f serverFlags
	var local bool
	cmd := &cobra.Command{
		Use:   "serve [dir]",
		Short: "Run the Cicord runtime (production)",
		Long: `Run the Cicord runtime: follow the configured source (cicord.yaml spec.source), reconcile
servers, run the bots and their plugins, and serve the HTTP API, the admin UI (/admin/) and
the community portal (/).

Environment:
  DISCORD_TOKEN_*             bot tokens (as named by token.secretRef)
  CICORD_GIT_TOKEN              HTTPS token for git fetch, write-back, pull requests and commit statuses
  CICORD_GITHUB_APP_ID          GitHub App ID: authenticate as a GitHub App instead (takes precedence)
  CICORD_GITHUB_APP_PRIVATE_KEY the App's private key (PEM), or --github-app-key-file
  CICORD_GITHUB_APP_INSTALLATION_ID  the App's installation (default: discovered from the repository)
  CICORD_RUNTIME_TOKEN          bearer token of the CLI / CI (machine endpoints)
  CICORD_WEBHOOK_SECRET         GitHub webhook secret
  CICORD_LINK_SECRET            portal link signing key (default: generated in the data dir)
  CICORD_DISCORD_CLIENT_SECRET  Discord OAuth2 client secret (admin UI / portal login)
  CICORD_DISCORD_CLIENT_ID      OAuth2 client ID (default: the first bot's application ID)
  CICORD_ALLOW_PRIVATE_REGISTRIES  "true" lets the Store and plugin downloads reach private networks
  CICORD_METRICS_LISTEN         address of the Prometheus metrics listener (--metrics-listen)

Bot tokens, CICORD_GIT_TOKEN, the GitHub App secrets, CICORD_RUNTIME_TOKEN,
CICORD_WEBHOOK_SECRET and CICORD_DISCORD_CLIENT_SECRET can instead be kept in the encrypted
secret store (cicord secret set NAME, or the setup wizard); the environment takes precedence.
The runtime token and the webhook secret are read at startup: restart after changing them.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			explicit := cmd.Flags().Changed("data-dir")
			if f.dataDir == "" {
				f.dataDir = ".cicord-data"
			}
			return runServe(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, f, local, explicit)
		},
	}
	f.register(cmd, ":7420")
	cmd.Flags().StringVar(&f.adminURL, "admin-url", os.Getenv("CICORD_ADMIN_URL"),
		"URL of the admin console on a separate (private) hostname, e.g. a VPN; the admin UI and admin API then only answer on that host (env CICORD_ADMIN_URL)")
	cmd.Flags().BoolVar(&local, "local", false, "serve the directory as-is, ignoring spec.source (no git)")
	cmd.Flags().StringVar(&f.githubAppKeyFile, "github-app-key-file", os.Getenv(gitauth.AppPrivateKeyFileEnv),
		"GitHub App private key file (PEM), instead of the "+gitauth.AppPrivateKeySecret+" secret (env "+gitauth.AppPrivateKeyFileEnv+")")
	cmd.Flags().StringVar(&f.metricsListen, "metrics-listen", os.Getenv("CICORD_METRICS_LISTEN"),
		"serve Prometheus metrics on GET /metrics of this address, without authentication, e.g. 127.0.0.1:9420 (default: disabled; env CICORD_METRICS_LISTEN)")
	return cmd
}

func runServe(ctx context.Context, stdout, stderr io.Writer, dir string, f serverFlags, local, explicitDataDir bool) error {
	log := newLogger(stderr, f.logLevel)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if f.adminURL, err = normalizeAdminURL(f.adminURL); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(absDir, "cicord.yaml")); errors.Is(err, os.ErrNotExist) && !local {
		// No configured repository: setup wizard (or the repository it created).
		return runServeSetup(ctx, stdout, log, absDir, f)
	}
	proj, err := readProject(absDir)
	if err != nil {
		return err
	}
	sp := proj.Spec.Source
	provider := ""
	if sp != nil && !local {
		provider = sp.Provider
	}
	dataDir, err := filepath.Abs(f.dataDir)
	if err != nil {
		return err
	}
	switch provider {
	case "github", "git", "embedded":
	default: // local source: the repository is watched
		if dataDir, err = checkLocalDataDir(absDir, f.dataDir, explicitDataDir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}

	// Source + write-back.
	var (
		src    source.Source
		writer writeback.Writer
		branch string
	)
	// Secrets: environment first, then the encrypted secret store of the data dir.
	store, err := openStateStore(ctx, log, dataDir, databaseURL(f.databaseURL))
	if err != nil {
		return err
	}
	defer store.Close()
	defer registry.UseSerialStore(registry.KVSerialStore(store.KV(registry.SerialKVScope)))() // anti-rollback of the official index
	sec, err := openSecrets(ctx, store, dataDir, false)
	if err != nil {
		return err
	}
	resolve := secrets.Resolver(sec, nil)
	var (
		tokens gitauth.TokenSource // credentials of a remote repository
		ghRepo string              // owner/name of a github provider (pull requests, commit statuses)
	)
	switch provider {
	case "github", "git", "embedded":
		remote, path := sp.Repo, sp.Path
		branch = sp.Branch
		if branch == "" {
			branch = "main"
		}
		if provider == "embedded" {
			remote = filepath.Join(dataDir, "repo.git")
			path = ""
			if err := ensureEmbeddedRepo(remote, absDir, branch); err != nil {
				return fmt.Errorf("embedded repository: %w", err)
			}
		}
		if remote == "" {
			return fmt.Errorf("cicord.yaml: spec.source.repo is required for provider %s", provider)
		}
		poll := time.Minute
		if sp.PollInterval != "" {
			if d, err := time.ParseDuration(sp.PollInterval); err == nil {
				poll = d
			}
		}
		if provider != "embedded" {
			method := ""
			tokens, method, err = gitauth.Resolve(gitauth.Options{Lookup: resolve, KeyFile: f.githubAppKeyFile, Repo: githubRepo(remote)})
			if err != nil {
				return err
			}
			logRepoAuth(log, method, remote)
		}
		g, err := source.NewGit(source.GitOptions{URL: remote, Branch: branch, Path: path,
			CacheDir: filepath.Join(dataDir, "source"), Tokens: tokens, PollInterval: poll})
		if err != nil {
			return err
		}
		src = g
		gw := writeback.GitOptions{URL: remote, Branch: branch, Path: path, WorkDir: filepath.Join(dataDir, "writeback"), Tokens: tokens}
		if provider == "github" {
			ghRepo = githubRepo(remote)
			gw.GitHubRepo = ghRepo
		}
		if proj.Spec.WriteBack != nil && proj.Spec.WriteBack.Branch != "" {
			gw.Branch = proj.Spec.WriteBack.Branch
		}
		writer = writeback.NewGitWriter(gw)
	default:
		l, err := source.NewLocal(absDir, filepath.Join(dataDir, "snapshots"))
		if err != nil {
			return err
		}
		src = l
		writer = &writeback.DirWriter{Root: absDir}
	}
	defer src.Close()

	key, err := linkKey(dataDir)
	if err != nil {
		return err
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", f.listen)
	if err != nil {
		return err
	}
	if f.publicURL == "" && proj.Spec.Runtime != nil && proj.Spec.Runtime.URL != "" {
		f.publicURL = proj.Spec.Runtime.URL // the operator's own cicord.yaml
	}
	if f.publicURL == "" {
		log.Warn("no --public-url (or cicord.yaml spec.runtime.url): OAuth redirects, portal links and the CSRF origin check use " +
			"http://<listen address>; set it to the URL users reach (e.g. https://cicord.example.org)")
	}
	public := publicURLFor(f.publicURL, ln)
	if err := checkAdminDistinct(f.adminURL, public); err != nil {
		_ = ln.Close()
		return err
	}
	console := consoleURL(f.adminURL, public)
	portalURL := public
	if proj.Spec.Portal != nil && proj.Spec.Portal.URL != "" {
		portalURL = proj.Spec.Portal.URL
	}

	cacheDir := registry.DefaultCacheDir()
	reg := newMetricsRegistry()
	rt := runtime.New(runtime.Options{
		Source: src, Connector: disgoadapter.NewConnector(disgoadapter.WithLogger(log)), Store: store, Secrets: resolve,
		Resolver: func(root string) config.PluginResolver { return registry.NewRuntimeResolver(root, cacheDir, resolve) },
		Signer:   auth.NewSigner(key), PortalURL: portalURL, Mode: "serve", Logger: log,
		CommitStatus: commitStatusHook(log, ghRepo, tokens, console), Metrics: reg,
		Analytics: store.Analytics(),
	})
	stopping := make(chan struct{})
	// Machine secrets: the environment first, then the encrypted secret store (cicord secret set).
	runtimeToken, _ := resolve("CICORD_RUNTIME_TOKEN")
	webhookSecret, _ := resolve("CICORD_WEBHOOK_SECRET")
	if runtimeToken == "" {
		log.Warn("CICORD_RUNTIME_TOKEN is not set: machine endpoints (cicord plan --remote, /api/v1/sync) are disabled")
	}
	cat := catalog.Official()
	h := api.New(api.Options{
		Runtime: rt, Writer: writer, Mode: "serve", RuntimeToken: runtimeToken,
		Analytics: store.Analytics(), AnalyticsStatus: rt.AnalyticsStatus,
		WebhookSecret: webhookSecret, Branch: branch,
		OAuth: api.OAuth{ClientID: os.Getenv("CICORD_DISCORD_CLIENT_ID"), ClientSecret: os.Getenv("CICORD_DISCORD_CLIENT_SECRET"),
			SecretFunc: setup.OAuthClientSecret(sec, nil)},
		PublicURL: public, AdminURL: f.adminURL, CORSOrigins: f.corsOrigins, AdminUI: webui.Admin(), Portal: webui.Portal(), Logger: log,
		Stopping: stopping, Checker: &setup.Checker{Runtime: rt, Probe: defaultProbe(), Secrets: resolve, Writer: writer},
		Catalog: cat, SecretStore: sec, Secret: resolve, PluginStore: runtimePluginStore(cat, resolve, stopping, rt.Repo),
		Metrics: reg,
	})
	stopMetrics, err := startMetrics(ctx, log, f.metricsListen, reg)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer stopMetrics()
	fmt.Fprintf(stdout, "Cicord runtime listening on %s\n  admin UI: %s/admin/\n  data: %s\n", public, console, dataDir)
	return runHTTP(ctx, log, ln, h, rt, stopping)
}
