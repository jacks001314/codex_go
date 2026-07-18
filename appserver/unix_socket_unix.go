//go:build !windows

package appserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

func serveUnixSocket(ctx context.Context, socketPath string, routerFactory func() *RuntimeRouter) error {
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

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		router := routerFactory()
		go func(conn net.Conn) {
			_ = serveUnixSocketConnection(conn, router)
		}(conn)
	}
}
