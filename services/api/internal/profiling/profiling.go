// Package profiling provides an opt-in, internal-only net/http/pprof server.
//
// Profiling is DISABLED by default. It is never mounted on the public API mux
// or port; when enabled it listens on a separate address that binds to
// localhost by default (127.0.0.1:6060), so it is not reachable from the public
// ingress. Enable it only for short, deliberate debugging sessions.
package profiling

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"time"
)

// DefaultAddr binds to the loopback interface so pprof is not publicly routable
// even when enabled. Override with PPROF_ADDR for an internal-only interface.
const DefaultAddr = "127.0.0.1:6060"

// Enabled reports whether pprof profiling has been explicitly turned on via
// PPROF_ENABLED. It is off unless PPROF_ENABLED is exactly "true".
func Enabled() bool {
	return os.Getenv("PPROF_ENABLED") == "true"
}

// Addr returns the address the pprof server listens on (PPROF_ADDR, or
// DefaultAddr).
func Addr() string {
	if a := os.Getenv("PPROF_ADDR"); a != "" {
		return a
	}
	return DefaultAddr
}

// Handler builds a mux with only the net/http/pprof endpoints mounted under
// /debug/pprof/. Exposed for testing.
func Handler() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

// Start launches the internal pprof server if PPROF_ENABLED=true and returns
// its *http.Server so the caller can shut it down. It returns nil when
// profiling is disabled — the default — in which case no listener is opened and
// no pprof route exists anywhere in the process.
func Start() *http.Server {
	if !Enabled() {
		return nil
	}

	addr := Addr()
	srv := &http.Server{
		Addr:              addr,
		Handler:           Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Warn("pprof profiling ENABLED — ensure this address is internal-only",
		"addr", addr)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("pprof server error", "err", err)
		}
	}()

	return srv
}

// Shutdown gracefully stops the pprof server if it is running (nil-safe).
func Shutdown(srv *http.Server) {
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
