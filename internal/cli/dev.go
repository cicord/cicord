package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cicord/cicord/internal/api"
	"github.com/cicord/cicord/internal/auth"
	"github.com/cicord/cicord/internal/catalog"
	"github.com/cicord/cicord/internal/config"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/discord/disgoadapter"
	"github.com/cicord/cicord/internal/registry"
	"github.com/cicord/cicord/internal/runtime"
	"github.com/cicord/cicord/internal/secrets"
	"github.com/cicord/cicord/internal/setup"
	"github.com/cicord/cicord/internal/sim"
	"github.com/cicord/cicord/internal/source"
	"github.com/cicord/cicord/internal/state"
	"github.com/cicord/cicord/internal/version"
	"github.com/cicord/cicord/internal/webui"
	"github.com/cicord/cicord/internal/writeback"
)

func newDevCmd() *cobra.Command {
	var f serverFlags
	var useSim, forceSetup bool
	cmd := &cobra.Command{
		Use:   "dev [dir]",
		Short: "Run the runtime locally with hot reload (real Discord or --sim)",
		Long: `Run the Cicord runtime on a local working directory: every saved change becomes a new
revision (hot reload), admin UI edits are written back to the files, and
POST /auth/dev-login signs in as any user.

With --sim, Discord is replaced by the in-memory simulator, seeded from the repository:
the servers (with their discordId), the bots (with their applicationId, invited with
Administrator), an "owner" user who owns every server and a few extra users. Open the
Simulator panel of the admin UI to play the users. Without --sim, the bot tokens are
read from the environment (token.secretRef), then from the runtime secret store.

In a directory without cicord.yaml, the runtime starts in setup mode: open the printed
/admin/setup link to create the configuration with the setup wizard (the files are
written into the directory). With --sim, the wizard runs against a simulated Discord
(a simulated bot, server and invite); --setup makes the intent explicit and fails when
the directory already has a cicord.yaml.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runDev(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, f, useSim, forceSetup, cmd.Flags().Changed("data-dir"))
		},
	}
	f.register(cmd, "127.0.0.1:7420")
	cmd.Flags().BoolVar(&useSim, "sim", false, "use the Discord simulator instead of real Discord")
	cmd.Flags().BoolVar(&forceSetup, "setup", false, "start the setup wizard (the directory must not contain cicord.yaml)")
	return cmd
}

func runDev(ctx context.Context, stdout, stderr io.Writer, dir string, f serverFlags, useSim, forceSetup, explicitDataDir bool) error {
	log := newLogger(stderr, f.logLevel)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	_, statErr := os.Stat(filepath.Join(absDir, "cicord.yaml"))
	hasProject := statErr == nil
	if forceSetup && hasProject {
		return fmt.Errorf("%s already contains a cicord.yaml: the setup wizard only runs on a directory without configuration", absDir)
	}
	if hasProject {
		if _, err := readProject(absDir); err != nil {
			return err
		}
	}
	if f.dataDir == "" {
		f.dataDir = filepath.Join(absDir, ".cicord", "dev")
	}
	if f.dataDir, err = checkLocalDataDir(absDir, f.dataDir, explicitDataDir); err != nil {
		return err
	}
	if !hasProject {
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			return err
		}
		return runDevSetup(ctx, stdout, log, absDir, f, useSim)
	}
	if err := os.MkdirAll(f.dataDir, 0o700); err != nil {
		return err
	}

	src, err := source.NewLocal(absDir, filepath.Join(f.dataDir, "snapshots"))
	if err != nil {
		return err
	}
	defer src.Close()

	// The simulator's IDs are new on every run: its state must not outlive the process.
	var store state.Store
	if useSim {
		store, err = openSimStore(log, f)
	} else {
		store, err = openStateStore(ctx, log, f.dataDir, databaseURL(f.databaseURL))
	}
	if err != nil {
		return err
	}
	defer store.Close()
	defer registry.UseSerialStore(registry.KVSerialStore(store.KV(registry.SerialKVScope)))() // anti-rollback of the official index
	sec, err := openSecrets(ctx, store, f.dataDir, useSim)
	if err != nil {
		return err
	}
	stored := secrets.Resolver(sec, nil) // environment first, then the secret store

	cacheDir := registry.DefaultCacheDir()
	resolver := func(root string) config.PluginResolver { return registry.NewRuntimeResolver(root, cacheDir, stored) }

	var (
		connector discord.Connector
		world     *sim.World
		seed      *api.SimSeed
		lookup    = stored
		probe     setup.Probe
	)
	if useSim {
		repo, ds := config.Load(absDir, config.LoadOptions{EngineVersion: version.Version, Resolver: resolver(absDir)})
		if repo == nil {
			printDiagnostics(stderr, ds)
			return fmt.Errorf("cannot load %s", absDir)
		}
		world = sim.NewWorld()
		if seed, err = api.SeedSimWorld(world, repo); err != nil {
			return fmt.Errorf("seed simulator: %w", err)
		}
		connector = world
		probe = setup.NewSimProbe(world)
		lookup = func(name string) (string, bool) {
			if v, ok := seed.Secrets[name]; ok {
				return v, true
			}
			return stored(name)
		}
	} else {
		connector = disgoadapter.NewConnector(disgoadapter.WithLogger(log))
		probe = defaultProbe()
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", f.listen)
	if err != nil {
		return err
	}
	public := publicURLFor(f.publicURL, ln)
	key, err := linkKey(f.dataDir)
	if err != nil {
		return err
	}
	rt := runtime.New(runtime.Options{
		Source: src, Connector: connector, Store: store, Secrets: lookup, Resolver: resolver,
		Signer: auth.NewSigner(key), PortalURL: public, Mode: "dev", Simulator: useSim,
		// Plugins may reach local test services only against the simulator: with real
		// Discord tokens the normal egress rules apply.
		AllowPrivateNetworks: useSim, Logger: log,
	})
	token := os.Getenv("CICORD_RUNTIME_TOKEN")
	generated := false
	if token == "" {
		token, generated = auth.RandomID(24), true
	}
	stopping := make(chan struct{})
	writer := &writeback.DirWriter{Root: absDir}
	env := &runtimeEnv{log: log, mode: "dev", dataDir: f.dataDir, dir: absDir, public: public, store: store, secrets: sec,
		resolve: lookup, connector: connector, probe: probe, world: world}
	var resumeOut strings.Builder
	wz, err := resumeWizard(ctx, env, &resumeOut) // setup wizard activated but not finished
	if err != nil {
		return err
	}
	checker := &setup.Checker{Runtime: rt, Probe: probe, Secrets: lookup, Writer: writer}
	if wz != nil {
		if err := wz.Resume(ctx, &setup.Activation{Runtime: rt, Writer: writer, Checker: checker}); err != nil {
			return err
		}
	}
	cat := catalog.Official()
	h := api.New(api.Options{
		Runtime: rt, Writer: writer, Mode: "dev", Sim: world,
		RuntimeToken: token, WebhookSecret: os.Getenv("CICORD_WEBHOOK_SECRET"),
		OAuth: api.OAuth{ClientID: os.Getenv("CICORD_DISCORD_CLIENT_ID"), ClientSecret: os.Getenv("CICORD_DISCORD_CLIENT_SECRET"),
			SecretFunc: setup.OAuthClientSecret(sec, nil)},
		PublicURL: public, CORSOrigins: f.corsOrigins, AdminUI: webui.Admin(), Portal: webui.Portal(), Logger: log,
		Stopping: stopping, Setup: wz, Checker: checker,
		Catalog: cat, SecretStore: sec, Secret: stored, PluginStore: runtimePluginStore(cat, stored, stopping, rt.Repo),
	})

	fmt.Fprintf(stdout, "Cicord dev runtime listening on %s\n", public)
	fmt.Fprintf(stdout, "  admin UI: %s/admin/\n", public)
	fmt.Fprintf(stdout, "  watching: %s (hot reload; admin UI edits are written to these files)\n", absDir)
	if generated {
		fmt.Fprintf(stdout, "  runtime token (CLI/CI bearer): %s\n", token)
	}
	fmt.Fprint(stdout, resumeOut.String())
	if useSim {
		fmt.Fprintf(stdout, "  Discord simulator: open the Simulator panel in the admin UI to act as users\n")
		fmt.Fprintf(stdout, "  dev login: POST /auth/dev-login {\"userId\": %q} (owner of every server)\n", seed.Owner.ID)
		for _, u := range seed.Users {
			fmt.Fprintf(stdout, "  simulated user %s: %s\n", u.Username, u.ID)
		}
	}
	return runHTTP(ctx, log, ln, h, rt, stopping)
}
