//go:build !windows

package appserverdaemon

import (
	"fmt"
	"os"
)

// publishDaemonRelease renames a staged package into its release path. POSIX
// rename is atomic and is not subject to the transient sharing violations the
// Windows path retries (Rust app-server-daemon prepare_install.rs, #50782).
func publishDaemonRelease(stage, release string) error {
	if err := os.Rename(stage, release); err != nil {
		return fmt.Errorf("failed to publish managed daemon release from %s to %s: %w", stage, release, err)
	}
	return nil
}
