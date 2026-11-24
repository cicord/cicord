package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
	)
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Check that a Cicord runtime answers /healthz (exit 0 when healthy, 1 otherwise)",
		Long: `Send GET to the health URL of a running Cicord runtime and exit 0 on HTTP 200, 1 otherwise
(error, timeout or any other status). Made for container health checks, for example in a
Dockerfile:

  HEALTHCHECK CMD ["/usr/local/bin/cicord", "healthcheck"]`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := healthcheck(cmd.Context(), target, timeout); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "unhealthy:", err)
				return exitError{code: 1}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "url", defaultHealthURL, "health URL to check")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "maximum time for the request")
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
