//go:build windows

package appserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"codex_go/codexuds"
)

// serveUnixSocket binds the Windows control socket after securing its private
// directory (Rust codex-uds + app-server-transport's control socket startup).
func serveUnixSocket(ctx context.Context, socketPath string, routerFactory func() *RuntimeRouter, access DaemonShutdownAccess, shutdown func()) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return fmt.Errorf("%w: socket path is empty", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := codexuds.CheckSocketPath(socketPath); err != nil {
		return err
	}
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(socketPath)); err != nil {
		return err
	}
	validated, guard, err := codexuds.ValidatePrivateSocketPath(socketPath)
	if err != nil {
		return err
	}
	if guard != nil {
		defer guard.Close()
	}
	if err := codexuds.PrepareControlSocketPath(validated); err != nil {
		return err
	}
	listener, err := net.Listen("unix", validated)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(validated)
	return serveUnixSocketHTTP(ctx, listener, routerFactory, access, shutdown)
}

// verifiedLocalDaemonSocket validates the private rendezvous directory that holds
// socketPath without creating or repairing it (Rust #54421's cfg(windows) arm:
// codex_uds::validate_private_socket_path). The guard only pins the directory for
// the caller; the TUI revalidates the directory and verifies the peer when it
// connects through the same local-daemon path as an implicit launch, so the
// validated path is all this check needs.
func verifiedLocalDaemonSocket(socketPath string) (string, bool) {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return "", false
	}
	validated, guard, err := codexuds.ValidatePrivateSocketPath(socketPath)
	if err != nil {
		return "", false
	}
	if guard != nil {
		_ = guard.Close()
	}
	validated = strings.TrimSpace(validated)
	if validated == "" {
		return "", false
	}
	return validated, true
}
