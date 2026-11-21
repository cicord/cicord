package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/npjonath/codi/internal/api"
	"github.com/npjonath/codi/internal/auth"
	"github.com/npjonath/codi/internal/config"
	"github.com/npjonath/codi/internal/discord"
	"github.com/npjonath/codi/internal/discord/disgoadapter"
	"github.com/npjonath/codi/internal/registry"
	"github.com/npjonath/codi/internal/runtime"
	"github.com/npjonath/codi/internal/sim"
	"github.com/npjonath/codi/internal/source"
	"github.com/npjonath/codi/internal/state"
	"github.com/npjonath/codi/internal/state/sqlite"
	"github.com/npjonath/codi/internal/version"
	"github.com/npjonath/codi/internal/webui"
	"github.com/npjonath/codi/internal/writeback"
)

func newDevCmd() *cobra.Command {
	var f serverFlags
	var useSim bool
	cmd := &cobra.Command{
		Use:   "dev [dir]",
		Short: "Run the runtime locally with hot reload (real Discord or --sim)",
		Long: `Run the CODI runtime on a local working directory: every saved change becomes a new
revision (hot reload), admin UI edits are written back to the files, and
POST /auth/dev-login signs in as any user.

With --sim, Discord is replaced by the in-memory simulator, seeded from the repository:
the servers (with their discordId), the bots (with their applicationId, invited with
Administrator), an "owner" user who owns every server and a few extra users. Open the
Simulator panel of the admin UI to play the users. Without --sim, the bot tokens are
read from the environment (token.secretRef).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runDev(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, f, useSim, cmd.Flags().Changed("data-dir"))
		},
	}
	f.register(cmd, "127.0.0.1:7420")
	cmd.Flags().BoolVar(&useSim, "sim", false, "use the Discord simulator instead of real Discord")
	return cmd
}

func runDev(ctx context.Context, stdout, stderr io.Writer, dir string, f serverFlags, useSim, explicitDataDir bool) error {
	log := newLogger(stderr, f.logLevel)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if _, err := readProject(absDir); err != nil {
		return err
	}
	if f.dataDir == "" {
		f.dataDir = filepath.Join(absDir, ".codi", "dev")
	}
	if f.dataDir, err = checkLocalDataDir(absDir, f.dataDir, explicitDataDir); err != nil {
		return err
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
		store, err = sqlite.Open(":memory:")
	} else {
		store, err = sqlite.Open(filepath.Join(f.dataDir, "codi.db"))
	}
	if err != nil {
		return err
	}
	defer store.Close()

	cacheDir := registry.DefaultCacheDir()
	resolver := func(root string) config.PluginResolver { return registry.NewResolver(root, cacheDir) }

	var (
		connector discord.Connector
		world     *sim.World
		seed      *api.SimSeed
		secrets   = os.LookupEnv
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
		secrets = func(name string) (string, bool) {
			if v, ok := seed.Secrets[name]; ok {
				return v, true
			}
			return os.LookupEnv(name)
		}
	} else {
		connector = disgoadapter.NewConnector(disgoadapter.WithLogger(log))
	}

	ln, err := net.Listen("tcp", f.listen)
	if err != nil {
		return err
	}
	public := publicURLFor(f.publicURL, ln)
	key, err := linkKey(f.dataDir)
	if err != nil {
		return err
	}
	rt := runtime.New(runtime.Options{
		Source: src, Connector: connector, Store: store, Secrets: secrets, Resolver: resolver,
		Signer: auth.NewSigner(key), PortalURL: public, Mode: "dev", Simulator: useSim,
		// Plugins may reach local test services only against the simulator: with real
		// Discord tokens the normal egress rules apply.
		AllowPrivateNetworks: useSim, Logger: log,
	})
	token := os.Getenv("CODI_RUNTIME_TOKEN")
	generated := false
	if token == "" {
		token, generated = auth.RandomID(24), true
	}
	stopping := make(chan struct{})
	h := api.New(api.Options{
		Runtime: rt, Writer: &writeback.DirWriter{Root: absDir}, Mode: "dev", Sim: world,
		RuntimeToken: token, WebhookSecret: os.Getenv("CODI_WEBHOOK_SECRET"),
		OAuth:     api.OAuth{ClientID: os.Getenv("CODI_DISCORD_CLIENT_ID"), ClientSecret: os.Getenv("CODI_DISCORD_CLIENT_SECRET")},
		PublicURL: public, CORSOrigins: f.corsOrigins, AdminUI: webui.Admin(), Portal: webui.Portal(), Logger: log,
		Stopping: stopping,
	})

	fmt.Fprintf(stdout, "CODI dev runtime listening on %s\n", public)
	fmt.Fprintf(stdout, "  admin UI: %s/admin/\n", public)
	fmt.Fprintf(stdout, "  watching: %s (hot reload; admin UI edits are written to these files)\n", absDir)
	if generated {
		fmt.Fprintf(stdout, "  runtime token (CLI/CI bearer): %s\n", token)
	}
	if useSim {
		fmt.Fprintf(stdout, "  Discord simulator: open the Simulator panel in the admin UI to act as users\n")
		fmt.Fprintf(stdout, "  dev login: POST /auth/dev-login {\"userId\": %q} (owner of every server)\n", seed.Owner.ID)
		for _, u := range seed.Users {
			fmt.Fprintf(stdout, "  simulated user %s: %s\n", u.Username, u.ID)
		}
	}
	return runHTTP(ctx, log, ln, h, rt, stopping)
}
