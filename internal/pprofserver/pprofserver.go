// Package pprofserver exposes Go runtime profiles on an opt-in loopback
// listener so long-running processes can be inspected without a rebuild.
package pprofserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"
)

// EnvAddr names the environment variable that enables the profiling listener.
// Its value must be a loopback host:port, for example "127.0.0.1:6060".
const EnvAddr = "CODEX_PPROF_ADDR"

// AddrFromEnv returns the profiling address configured in the environment.
// Empty or blank values disable the listener.
func AddrFromEnv(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	return strings.TrimSpace(getenv(EnvAddr))
}

// Start binds addr and serves the standard /debug/pprof endpoints until ctx is
// done. Only loopback addresses are accepted so profiles never leave the host.
func Start(ctx context.Context, addr string) (net.Addr, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("pprof address is empty")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid pprof address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("pprof address %q must bind to a loopback IP", addr)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for pprof: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		// The listener is bound above, so Serve only fails once the server is
		// shut down; the process-lifetime goroutine does not need to report it.
		_ = server.Serve(listener)
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	return listener.Addr(), nil
}
