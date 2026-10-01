//go:build !windows

package appserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
)

func serveUnixSocket(ctx context.Context, socketPath string, routerFactory func() *RuntimeRouter, access DaemonShutdownAccess, shutdown func()) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return fmt.Errorf("%w: socket path is empty", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensureUnixSocketParent(socketPath); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	return serveUnixSocketHTTP(ctx, listener, routerFactory, access, shutdown)
}
