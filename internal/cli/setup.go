package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/npjonath/codi/internal/api"
	"github.com/npjonath/codi/internal/auth"
	"github.com/npjonath/codi/internal/catalog"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/discord/disgoadapter"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/secrets"
	"github.com/npjonath/codi/internal/setup"
	"github.com/npjonath/codi/internal/sim"
	"github.com/npjonath/codi/internal/source"
	"github.com/npjonath/codi/internal/state"
	"github.com/npjonath/codi/internal/state/sqlite"
	"github.com/npjonath/codi/internal/webui"
	"github.com/npjonath/codi/internal/writeback"
)

// Setup mode (docs/onboarding-wizard.md). Boot decision:
//
//	codi.yaml in the directory                     → normal boot (never setup mode)
//	no codi.yaml, setup state "activated"           → boot from the repository the wizard created
//	                                                  (serve: embedded / GitHub), wizard resumable
//	otherwise                                       → setup mode (wizard, resumable)

// swapHandler serves the current handler; the setup wizard swaps the setup-mode handler
// for the full API once the runtime is active (no process restart).
type swapHandler struct{ h atomic.Pointer[http.Handler] }

func newSwapHandler(h http.Handler) *swapHandler {
	s := &swapHandler{}
	s.set(h)
	return s
}

func (s *swapHandler) set(h http.Handler) { s.h.Store(&h) }

func (s *swapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.h.Load()).ServeHTTP(w, r)
}

// lateRuntime runs a runtime that is created after the HTTP server started.
type lateRuntime struct {
	mu      sync.Mutex
	ctx     context.Context
	done    chan error
	closers []func()
	started bool
}

func (l *lateRuntime) Run(ctx context.Context) error {
	l.mu.Lock()
	l.ctx = ctx
	l.mu.Unlock()
	<-ctx.Done()
	l.mu.Lock()
	done, closers := l.done, l.closers
	l.mu.Unlock()
	if done != nil {
		<-done
	}
	for _, c := range closers {
		c()
	}
	return nil
}

// start runs rt until the server stops; close is called after it stopped.
func (l *lateRuntime) start(rt *runtime.Runtime, closers ...func()) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx == nil {
		return errors.New("server not running")
	}
	if l.started {
		return errors.New("the runtime is already running")
	}
	l.started = true
	l.done = make(chan error, 1)
	l.closers = closers
	go func() { l.done <- rt.Run(l.ctx) }()
	return nil
}

// waitReady waits for the first revision to be active.
func waitReady(ctx context.Context, rt *runtime.Runtime, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		if rt.Ready() {
			return nil
		}
		if e := rt.Status().LastError; e != "" {
			return errors.New(e)
		}
		select {
		case <-ctx.Done():
			return errors.New("the runtime did not activate the configuration in time")
		case <-t.C:
		}
	}
}

// openSecrets opens the secret store of a data dir (key: CODI_SECRET_KEY or
// <data dir>/secret.key). ephemeral uses a random in-memory key (simulator).
func openSecrets(ctx context.Context, store state.Store, dataDir string, ephemeral bool) (*secrets.Store, error) {
	var key []byte
	var err error
	if ephemeral {
		key = auth.RandomKey()
	} else if key, _, err = secrets.LoadKey(dataDir, nil); err != nil {
		return nil, err
	}
	return secrets.Open(ctx, store, key)
}

// runtimeEnv holds what is needed to build a runtime + API once a repository exists.
type runtimeEnv struct {
	log        *slog.Logger
	mode       string // serve | dev
	dataDir    string
	dir        string // the target directory
	public     string
	cors       []string
	store      state.Store
	secrets    *secrets.Store
	resolve    func(string) (string, bool)
	connector  discord.Connector
	probe      setup.Probe
	world      *sim.World
	stopping   chan struct{}
	runtimeTok string
}

// build creates the source, writer and runtime for a repository chosen in the wizard.
func (e *runtimeEnv) build(repo setup.RepoConfig) (*runtime.Runtime, writeback.Writer, source.Source, string, error) {
	var (
		src    source.Source
		writer writeback.Writer
		branch = repo.Branch
	)
	if branch == "" {
		branch = "main"
	}
	switch repo.Provider {
	case setup.ProviderLocal:
		l, err := source.NewLocal(repo.Dir, filepath.Join(e.dataDir, "snapshots"))
		if err != nil {
			return nil, nil, nil, "", err
		}
		src, writer = l, &writeback.DirWriter{Root: repo.Dir}
	case setup.ProviderEmbedded, setup.ProviderGitHub:
		remote, token := repo.URL, ""
		if repo.Provider == setup.ProviderEmbedded {
			remote = repo.Dir
		} else if repo.TokenRef != "" {
			token, _ = e.resolve(repo.TokenRef)
		}
		g, err := source.NewGit(source.GitOptions{URL: remote, Branch: branch, CacheDir: filepath.Join(e.dataDir, "source"),
			Token: token, PollInterval: time.Minute})
		if err != nil {
			return nil, nil, nil, "", err
		}
		src = g
		gw := writeback.GitOptions{URL: remote, Branch: branch, WorkDir: filepath.Join(e.dataDir, "writeback"), Token: token}
		if repo.Provider == setup.ProviderGitHub {
			gw.GitHubRepo = setup.GitHubRepo(remote)
		}
		writer = writeback.NewGitWriter(gw)
	default:
		return nil, nil, nil, "", fmt.Errorf("unknown repository provider %q", repo.Provider)
	}
	key, err := linkKey(e.dataDir)
	if err != nil {
		src.Close()
		return nil, nil, nil, "", err
	}
	cacheDir := registry.DefaultCacheDir()
	rt := runtime.New(runtime.Options{
		Source: src, Connector: e.connector, Store: e.store, Secrets: e.resolve,
		Resolver: func(root string) config.PluginResolver { return registry.NewRuntimeResolver(root, cacheDir) },
		Signer:   auth.NewSigner(key), PortalURL: e.public, Mode: e.mode, Simulator: e.world != nil,
		AllowPrivateNetworks: e.world != nil, Logger: e.log,
	})
	return rt, writer, src, branch, nil
}

func (e *runtimeEnv) checker(rt *runtime.Runtime, w writeback.Writer) *setup.Checker {
	return &setup.Checker{Runtime: rt, Probe: e.probe, Secrets: e.resolve, Writer: w}
}

// apiOptions are the full API options for a running runtime.
func (e *runtimeEnv) apiOptions(rt *runtime.Runtime, w writeback.Writer, branch string, wz *setup.Wizard) api.Options {
	return api.Options{
		Runtime: rt, Writer: w, Mode: e.mode, Sim: e.world, RuntimeToken: e.runtimeTok,
		WebhookSecret: os.Getenv("CODI_WEBHOOK_SECRET"), Branch: branch,
		OAuth: api.OAuth{ClientID: os.Getenv("CODI_DISCORD_CLIENT_ID"), ClientSecret: os.Getenv("CODI_DISCORD_CLIENT_SECRET"),
			SecretFunc: setup.OAuthClientSecret(e.secrets, nil)},
		PublicURL: e.public, CORSOrigins: e.cors, AdminUI: webui.Admin(), Portal: webui.Portal(), Logger: e.log,
		Stopping: e.stopping, Setup: wz, Checker: e.checker(rt, w), Catalog: catalog.Official(),
	}
}

// setupFlags are the options of a setup-mode start.
type setupStart struct {
	env       *runtimeEnv
	ln        net.Listener
	stdout    io.Writer
	providers []string
	sim       *setup.SimEnv
}

// runSetup serves the wizard, then the full API once the wizard activated a repository.
func runSetup(ctx context.Context, ss setupStart) error {
	e := ss.env
	late := &lateRuntime{}
	sw := newSwapHandler(http.NotFoundHandler())
	var (
		wz      *setup.Wizard
		started struct {
			rt     *runtime.Runtime
			w      writeback.Writer
			branch string
		}
	)
	activate := func(actx context.Context, repo setup.RepoConfig) (*setup.Activation, error) {
		// A retry (the first revision was rejected or slow) reuses the running runtime: it
		// keeps following the repository and activates the next good revision.
		rt, w, branch := started.rt, started.w, started.branch
		if rt == nil {
			var src source.Source
			var err error
			if rt, w, src, branch, err = e.build(repo); err != nil {
				return nil, err
			}
			if err := late.start(rt, func() { src.Close() }); err != nil {
				src.Close()
				return nil, err
			}
			started.rt, started.w, started.branch = rt, w, branch
		} else {
			rt.Trigger()
		}
		if err := waitReady(actx, rt, 45*time.Second); err != nil {
			return nil, err
		}
		sw.set(api.New(e.apiOptions(rt, w, branch, wz)))
		e.log.Info("setup: configuration active", "provider", repo.Provider)
		return &setup.Activation{Runtime: rt, Writer: w, Checker: e.checker(rt, w)}, nil
	}
	wz = setup.New(setup.Options{Mode: e.mode, Store: e.store, Secrets: e.secrets, Connector: e.connector, Probe: e.probe,
		Catalog: catalog.Official(), Providers: ss.providers, DataDir: e.dataDir, Dir: e.dir, PublicURL: e.public,
		Activate: activate, Sim: ss.sim, Logger: e.log})
	sw.set(api.NewSetup(api.Options{Store: e.store, Mode: e.mode, PublicURL: e.public, CORSOrigins: e.cors,
		AdminUI: webui.Admin(), Logger: e.log, Stopping: e.stopping, Setup: wz, Catalog: catalog.Official()}))
	token := wz.IssueToken()
	url := setup.SetupURL(e.public, token)
	fmt.Fprintf(ss.stdout, "CODI is not set up yet: setup mode on %s\n", e.public)
	fmt.Fprintf(ss.stdout, "  open the setup wizard: %s\n", url)
	fmt.Fprintf(ss.stdout, "  (this link works once; restart CODI to get a new one)\n  data: %s\n", e.dataDir)
	if ss.sim != nil {
		fmt.Fprintf(ss.stdout, "  Discord simulator: the wizard creates a simulated bot and server for you\n")
	}
	e.log.Info("setup mode: waiting for the setup wizard", "url", e.public+"/admin/setup")
	return runHTTP(ctx, e.log, ss.ln, sw, late, e.stopping)
}

// resumeWizard returns a wizard attached to a running runtime when the setup state says
// the wizard activated a configuration but was not finished (restart in the middle of
// the Verify / First plugin steps); nil otherwise.
func resumeWizard(ctx context.Context, e *runtimeEnv, stdout io.Writer) (*setup.Wizard, error) {
	st, found, err := setup.LoadState(ctx, e.store)
	if err != nil || !found || !(st.Activated || st.RepoInitialized) || st.Completed {
		return nil, err
	}
	wz := setup.New(setup.Options{Mode: e.mode, Store: e.store, Secrets: e.secrets, Connector: e.connector, Probe: e.probe,
		Catalog: catalog.Official(), DataDir: e.dataDir, Dir: e.dir, PublicURL: e.public, Logger: e.log})
	tok := wz.IssueToken()
	fmt.Fprintf(stdout, "  setup wizard not finished: %s\n", setup.SetupURL(e.public, tok))
	return wz, nil
}

// dataDirInUse reports why a data dir without setup state belongs to an existing
// deployment ("" when it does not): setup mode must never start on it, since the wizard
// would rewrite its resource map.
func dataDirInUse(ctx context.Context, store state.Store, dataDir string) string {
	if recs, err := store.Syncs().List(ctx, "", 1); err == nil && len(recs) > 0 {
		return "it has a sync history"
	}
	if _, err := os.Stat(filepath.Join(dataDir, "repo.git")); err == nil {
		return "it contains an embedded repository (repo.git)"
	}
	return ""
}

func defaultProbe() setup.Probe { return &setup.HTTPProbe{} }

func defaultConnector(log *slog.Logger) discord.Connector {
	return disgoadapter.NewConnector(disgoadapter.WithLogger(log))
}

// serveDiscord returns the Discord connector and probe of `codi serve` setup mode
// (tests replace it with the simulator).
var serveDiscord = func(log *slog.Logger) (discord.Connector, setup.Probe) {
	return defaultConnector(log), defaultProbe()
}

// runServeSetup is `codi serve` in a directory without codi.yaml.
func runServeSetup(ctx context.Context, stdout io.Writer, log *slog.Logger, absDir string, f serverFlags) error {
	dataDir, err := filepath.Abs(f.dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	store, err := sqlite.Open(filepath.Join(dataDir, "codi.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	sec, err := openSecrets(ctx, store, dataDir, false)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", f.listen)
	if err != nil {
		return err
	}
	public := publicURLFor(f.publicURL, ln)
	runtimeTok := os.Getenv("CODI_RUNTIME_TOKEN")
	e := &runtimeEnv{log: log, mode: "serve", dataDir: dataDir, dir: absDir, public: public, cors: f.corsOrigins, store: store,
		secrets: sec, resolve: secrets.Resolver(sec, nil), stopping: make(chan struct{}), runtimeTok: runtimeTok}
	e.connector, e.probe = serveDiscord(log)

	st, found, err := setup.LoadState(ctx, store)
	if err != nil {
		return err
	}
	if !found {
		if why := dataDirInUse(ctx, store, dataDir); why != "" {
			return fmt.Errorf("%s has no codi.yaml, but the data dir %s belongs to an existing deployment (%s): "+
				"run codi serve in the configuration repository, or use another --data-dir to set up a new one", absDir, dataDir, why)
		}
	}
	if st.Activated && st.Repo != nil && st.Repo.Provider != setup.ProviderLocal {
		if f.publicURL == "" && st.PublicURL != "" {
			public = st.PublicURL // the URL the wizard was set up with (OAuth redirect)
			e.public = public
		}
		// The wizard created the repository: boot from it.
		rt, w, src, branch, err := e.build(*st.Repo)
		if err != nil {
			return err
		}
		defer src.Close()
		wz, err := resumeWizard(ctx, e, stdout)
		if err != nil {
			return err
		}
		if wz != nil {
			if err := wz.Resume(ctx, &setup.Activation{Runtime: rt, Writer: w, Checker: e.checker(rt, w)}); err != nil {
				return err
			}
		}
		if runtimeTok == "" {
			log.Warn("CODI_RUNTIME_TOKEN is not set: machine endpoints (codi plan --remote, /api/v1/sync) are disabled")
		}
		fmt.Fprintf(stdout, "CODI runtime listening on %s\n  admin UI: %s/admin/\n  configuration: %s repository (set up by the wizard)\n  data: %s\n",
			public, public, st.Repo.Provider, dataDir)
		return runHTTP(ctx, log, ln, api.New(e.apiOptions(rt, w, branch, wz)), rt, e.stopping)
	}
	if f.publicURL == "" {
		log.Warn("no --public-url: the setup wizard uses " + public + " for the admin login redirect; set it to the URL people reach")
	}
	return runSetup(ctx, setupStart{env: e, ln: ln, stdout: stdout, providers: []string{setup.ProviderEmbedded, setup.ProviderGitHub}})
}

// runDevSetup is `codi dev` in a directory without codi.yaml (--sim: simulated Discord).
func runDevSetup(ctx context.Context, stdout io.Writer, log *slog.Logger, absDir string, f serverFlags, useSim bool) error {
	if err := os.MkdirAll(f.dataDir, 0o700); err != nil {
		return err
	}
	var (
		store state.Store
		err   error
	)
	if useSim {
		store, err = sqlite.Open(":memory:") // the simulator lives as long as the process
	} else {
		store, err = sqlite.Open(filepath.Join(f.dataDir, "codi.db"))
	}
	if err != nil {
		return err
	}
	defer store.Close()
	if !useSim {
		if _, found, err := setup.LoadState(ctx, store); err == nil && !found {
			if why := dataDirInUse(ctx, store, f.dataDir); why != "" {
				return fmt.Errorf("%s has no codi.yaml, but the data dir %s belongs to an existing deployment (%s)", absDir, f.dataDir, why)
			}
		}
	}
	sec, err := openSecrets(ctx, store, f.dataDir, useSim)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", f.listen)
	if err != nil {
		return err
	}
	public := publicURLFor(f.publicURL, ln)
	tok := os.Getenv("CODI_RUNTIME_TOKEN")
	if tok == "" {
		tok = auth.RandomID(24)
	}
	e := &runtimeEnv{log: log, mode: "dev", dataDir: f.dataDir, dir: absDir, public: public, cors: f.corsOrigins, store: store,
		secrets: sec, resolve: secrets.Resolver(sec, nil), connector: defaultConnector(log), probe: defaultProbe(),
		stopping: make(chan struct{}), runtimeTok: tok}
	var env *setup.SimEnv
	if useSim {
		w := sim.NewWorld()
		if env, err = setup.NewSimEnv(w); err != nil {
			return err
		}
		e.world, e.connector, e.probe = w, w, env.Probe()
	}
	return runSetup(ctx, setupStart{env: e, ln: ln, stdout: stdout, providers: []string{setup.ProviderLocal, setup.ProviderEmbedded}, sim: env})
}
