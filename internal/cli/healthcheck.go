package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	extraCommands = append(extraCommands, newHealthcheckCmd)
}

// defaultHealthURL is /healthz on the default --listen port of `cicord serve`.
const defaultHealthURL = "http://127.0.0.1:7420/healthz"

// newHealthcheckCmd is `cicord healthcheck`: a probe for container health checks (the
// distroless image has no curl), e.g. HEALTHCHECK CMD ["/usr/local/bin/cicord", "healthcheck"].
func newHealthcheckCmd() *cobra.Command {
	var (
		target  string
		timeout time.Duration
		service bool
		dataDir string
		dbFlag  string
		admin   string
	)
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Check that a Cicord runtime answers /healthz, or (--service) that it serves every server (exit 0 when healthy)",
		Long: `Send GET to the health URL of a running Cicord runtime and exit 0 on HTTP 200, 1 otherwise
(error, timeout or any other status). Made for container health checks, for example in a
Dockerfile:

  HEALTHCHECK CMD ["/usr/local/bin/cicord", "healthcheck"]

--service checks the service, not only the process: it runs ` + "`cicord status --wait`" + ` against
the runtime of --url, so it exits 0 once the active revision is loaded, every server is Synced
and Healthy (changes that wait for a boost level are fine), no server is paused, and every
bot and plugin instance is Healthy; 1 when one of them fails or --timeout (default 5m with
--service) expires. It authenticates with the runtime token: CICORD_RUNTIME_TOKEN, else the
secret store of --data-dir. When the runtime has an admin URL (--admin-url, default
CICORD_ADMIN_URL), the requests carry its host, as the admin API only answers there. Exit 2
when no runtime token is available. The VPS updater accepts a new version with it:

  docker exec cicord cicord healthcheck --service --data-dir /data --timeout 5m`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if service {
				if !cmd.Flags().Changed("timeout") {
					timeout = 5 * time.Minute
				}
				return serviceCheck(cmd, target, timeout, dataDir, dbFlag, admin)
			}
			if err := healthcheck(cmd.Context(), target, timeout); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "unhealthy:", err)
				return exitError{code: 1}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "url", defaultHealthURL, "health URL to check (--service: the runtime of this URL)")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "maximum time for the request (--service: for the wait, default 5m)")
	cmd.Flags().BoolVar(&service, "service", false, "check the service: active revision, servers Synced and Healthy, bots and plugins Healthy")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "with --service: runtime state directory, to read the runtime token from the secret store")
	cmd.Flags().StringVar(&dbFlag, "database-url", "", "with --service: "+databaseURLFlagHelp)
	cmd.Flags().StringVar(&admin, "admin-url", os.Getenv("CICORD_ADMIN_URL"), "with --service: the runtime's admin URL (env CICORD_ADMIN_URL)")
	return cmd
}

// healthcheck returns nil when GET target answers 200 within timeout. Redirects are not
// followed (the health endpoint never redirects).
func healthcheck(ctx context.Context, target string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("invalid --url: %w", err)
	}
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", target, res.Status)
	}
	return nil
}

// serviceCheck is `cicord healthcheck --service`: `cicord status --wait` against the
// runtime of target, with the runtime token of the environment or the secret store.
func serviceCheck(cmd *cobra.Command, target string, timeout time.Duration, dataDir, dbFlag, admin string) error {
	stderr := cmd.ErrOrStderr()
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid --url %q", target)
	}
	base := u.Scheme + "://" + u.Host
	if err := healthcheck(cmd.Context(), base+"/healthz", 10*time.Second); err != nil {
		fmt.Fprintln(stderr, "unhealthy:", err)
		return exitError{code: 1}
	}
	token := os.Getenv("CICORD_RUNTIME_TOKEN")
	if token == "" && dataDir != "" {
		s, done, err := openDataDirSecrets(cmd.Context(), dataDir, dbFlag)
		if err != nil {
			fmt.Fprintln(stderr, "cannot read the runtime token:", err)
			return exitError{code: 2}
		}
		token, _ = s.Get(cmd.Context(), "CICORD_RUNTIME_TOKEN")
		done()
	}
	if token == "" {
		fmt.Fprintln(stderr, "no runtime token: set CICORD_RUNTIME_TOKEN, or store it with `cicord secret set CICORD_RUNTIME_TOKEN` (and restart the runtime)")
		return exitError{code: 2}
	}
	if admin = strings.TrimSpace(admin); admin != "" {
		// The admin API only answers on the admin host: a local proxy gives the requests
		// that Host (the network boundary stays the loopback listener).
		proxied, stop, err := adminHostProxy(base, admin)
		if err != nil {
			return err
		}
		defer stop()
		base = proxied
	}
	restore := setEnv("CICORD_RUNTIME_TOKEN", token)
	defer restore()
	status := newStatusCmd()
	status.SetArgs([]string{"--wait", "--timeout", timeout.String(), "--remote=" + base})
	status.SetOut(cmd.OutOrStdout())
	status.SetErr(stderr)
	status.SilenceUsage, status.SilenceErrors = true, true
	if err := status.ExecuteContext(cmd.Context()); err != nil {
		var ee exitError
		if !errors.As(err, &ee) {
			fmt.Fprintln(stderr, "not healthy:", err)
		}
		return exitError{code: 1}
	}
	return nil
}

// setEnv sets an environment variable and returns the function that restores it.
func setEnv(name, value string) func() {
	old, had := os.LookupEnv(name)
	_ = os.Setenv(name, value)
	return func() {
		if had {
			_ = os.Setenv(name, old)
		} else {
			_ = os.Unsetenv(name)
		}
	}
}

// adminHostProxy serves a loopback proxy to base that sends every request with the host of
// admin, and returns its URL.
func adminHostProxy(base, admin string) (string, func(), error) {
	au, err := url.Parse(admin)
	if err != nil || au.Host == "" {
		return "", nil, fmt.Errorf("invalid --admin-url %q", admin)
	}
	bu, err := url.Parse(base)
	if err != nil {
		return "", nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	rp := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(bu)
		r.Out.Host = au.Host
	}}
	srv := &http.Server{Handler: rp, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String(), func() { _ = srv.Close() }, nil
}
