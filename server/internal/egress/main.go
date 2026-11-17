package egress

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Sidecar settings, set by the server when it creates the proxy container.
const (
	EnvAllow        = "EGRESS_ALLOW"         // comma-separated allowlist (expanded)
	EnvAllowPrivate = "EGRESS_ALLOW_PRIVATE" // hosts that may be private (the gateway)
	EnvReportURL    = "EGRESS_REPORT_URL"    // gateway egress/blocked URL
	EnvReportToken  = "EGRESS_REPORT_TOKEN"  // the run's gateway token
	EnvListen       = "EGRESS_LISTEN"        // default :3128
)

// Port is where the proxy listens in the task network.
const Port = "3128"

// RunProxy runs the proxy from its environment until ctx ends.
func RunProxy(ctx context.Context, cancel context.CancelFunc) error {
	defer cancel()
	p := &Proxy{
		Allow:        NewAllowlist(splitEnv(EnvAllow)),
		AllowPrivate: splitEnv(EnvAllowPrivate),
	}
	if u, tok := os.Getenv(EnvReportURL), os.Getenv(EnvReportToken); u != "" && tok != "" {
		p.Blocked = Reporter(u, tok)
	}
	addr := os.Getenv(EnvListen)
	if addr == "" {
		addr = ":" + Port
	}
	srv := &http.Server{Addr: addr, Handler: p, ReadHeaderTimeout: 30 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("egress proxy listening", "addr", addr, "allow", len(p.Allow.entries))
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Health dials the local proxy (the sidecar's health check).
func Health() error {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+Port, 2*time.Second)
	if err != nil {
		return err
	}
	return c.Close()
}

func splitEnv(k string) []string {
	var out []string
	for _, p := range strings.Split(os.Getenv(k), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
