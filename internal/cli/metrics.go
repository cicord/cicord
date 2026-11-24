package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/cicord/cicord/internal/metrics"
	"github.com/cicord/cicord/internal/version"
)

// Prometheus metrics of `cicord serve` (https://cicord.dev/docs/self-hosting/monitoring/).
//
// They are served on a dedicated listener (--metrics-listen, off by default), never by the
// API listener: that one is behind the public reverse proxy, its admin-host restriction
// relies on the Host header (a scraper on the same machine sends 127.0.0.1:7420, not the
// admin hostname), and the runtime token would give the scraper the machine endpoints too
// (sync, plan uploads). The metrics listener has no authentication: the network is the
// boundary, so it belongs on a loopback address (or a port published on the host's
// loopback only, in a container).

// newMetricsRegistry returns the registry of the runtime, with the build, Go runtime and
// process metrics.
func newMetricsRegistry() *metrics.Registry {
	reg := metrics.NewRegistry()
	metrics.RegisterBuildInfo(reg, version.Version, version.Commit)
	metrics.RegisterGo(reg)
	metrics.RegisterProcess(reg)
	return reg
}

// metricsHandler answers GET /metrics; every other path is a 404.
func metricsHandler(reg *metrics.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler())
	return mux
}

// startMetrics serves reg on addr ("" disables it) until stop is called. A bind failure
// is returned (the runtime does not start without the metrics it was asked for).
func startMetrics(ctx context.Context, log *slog.Logger, addr string, reg *metrics.Registry) (stop func(), err error) {
	if addr == "" {
		return func() {}, nil
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("--metrics-listen: %w", err)
	}
	if tcp, ok := ln.Addr().(*net.TCPAddr); !ok || !tcp.IP.IsLoopback() {
		log.Warn("the metrics listener is not bound to a loopback address and has no authentication: "+
			"make sure only the scraper can reach it (in a container, publish it on the host's loopback only, e.g. 127.0.0.1:9420:9420)",
			"addr", ln.Addr().String())
	}
	srv := &http.Server{
		Handler:           metricsHandler(reg),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics listener stopped", "err", err)
		}
	}()
	log.Info("serving metrics", "url", "http://"+ln.Addr().String()+"/metrics")
	return func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}, nil
}
