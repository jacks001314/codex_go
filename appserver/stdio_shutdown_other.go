//go:build !unix

package appserver

import "context"

// installStdioShutdownSignals is a no-op on platforms without Unix SIGTERM
// (Rust #44523 handles signals on Unix builds only).
func installStdioShutdownSignals(ctx context.Context, cancel context.CancelFunc, arm func()) func() {
	return func() {}
}
