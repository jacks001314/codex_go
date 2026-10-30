//go:build windows

package appserverdaemon

import (
	"errors"
	"syscall"
)

// manualUpdateConnectionReset reports a socket reset or abort from the peer.
// Windows reports a closed unix socket peer as WSAECONNRESET, which is not the
// invented syscall.ECONNRESET value used for portability.
func manualUpdateConnectionReset(err error) bool {
	return errors.Is(err, syscall.WSAECONNRESET) ||
		errors.Is(err, syscall.WSAECONNABORTED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED)
}
