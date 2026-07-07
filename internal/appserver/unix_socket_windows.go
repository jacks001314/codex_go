//go:build windows

package appserver

import (
	"context"
)

func serveUnixSocket(ctx context.Context, socketPath string, routerFactory func() *RuntimeRouter) error {
	return ErrUnixSocketUnsupported
}
