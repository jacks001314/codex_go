//go:build windows

package mxc

import (
	"errors"
	"fmt"
	"syscall"
)

// nativeProcessLaunchError retains the Win32 code without adding command,
// environment, or working-directory diagnostics to the launch failure.
func nativeProcessLaunchError(err error) error {
	var code syscall.Errno
	if errors.As(err, &code) {
		return fmt.Errorf("CreateProcessW(SecurityEnvironment): %w (CreateProcessW, Win32 error %d)", err, uint32(code))
	}
	return fmt.Errorf("CreateProcessW(SecurityEnvironment): %w", err)
}
