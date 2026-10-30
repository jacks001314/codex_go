//go:build !windows

package appserverdaemon

import (
	"errors"
	"syscall"
)

// manualUpdateConnectionReset reports a socket reset or abort from the peer.
func manualUpdateConnectionReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED)
}
