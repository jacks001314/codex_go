//go:build !windows

package app

import (
	"errors"
	"io"
	"net"
	"strings"
)

func bridgeStdioToUDS(socketPath string, stdin io.Reader, stdout io.Writer) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return errors.New("stdio-to-uds requires SOCKET_PATH")
	}
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	errCh := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(conn, stdin)
		if unixConn, ok := conn.(*net.UnixConn); ok {
			_ = unixConn.CloseWrite()
		}
		errCh <- copyErr
	}()
	go func() {
		_, copyErr := io.Copy(stdout, conn)
		errCh <- copyErr
	}()

	var firstErr error
	for i := 0; i < 2; i++ {
		if copyErr := <-errCh; copyErr != nil && !errors.Is(copyErr, net.ErrClosed) && firstErr == nil {
			firstErr = copyErr
		}
	}
	return firstErr
}
