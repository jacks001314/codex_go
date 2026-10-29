//go:build !windows

package mxc

import (
	"errors"
	"io"
)

// helperSupported is false off Windows: the native MXC sandbox is a Windows
// process security environment.
const helperSupported = false

// runHelper is unreachable because RunMain reports the platform requirement
// first; it exists so the entry point keeps one shape on every host.
func runHelper(stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	return 0, errors.New(UnsupportedPlatformMessage)
}
