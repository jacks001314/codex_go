//go:build unix

package appserver

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// installStdioShutdownSignals runs connection cleanup on Unix SIGTERM (Rust
// #44523): the watchdog is armed before the stdio context is cancelled so a
// stalled teardown still exits with status 1.
func installStdioShutdownSignals(ctx context.Context, cancel context.CancelFunc, arm func()) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
		case <-signals:
			if arm != nil {
				arm()
			}
			cancel()
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}
