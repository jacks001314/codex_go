//go:build !windows

package windowsandboxservice

import "errors"

// runServiceMode refuses to host the service off Windows (Rust lib::run).
func runServiceMode(RunMode) error {
	return errors.New("the Codex sandbox service is only available on Windows")
}
