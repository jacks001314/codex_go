//go:build windows

package appserverdaemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	publishReleaseRetryInterval = 50 * time.Millisecond
	publishReleaseRetryLimit    = 100
)

// publishDaemonRelease renames a staged package into its release path. Executable
// scanners can briefly hold newly staged files without delete sharing, causing
// the rename to fail, so transient Windows sharing/permission errors are retried
// (Rust app-server-daemon prepare_install_windows::publish_release, #50782).
func publishDaemonRelease(stage, release string) error {
	retries := 0
	for {
		err := os.Rename(stage, release)
		if err == nil {
			return nil
		}
		if retries < publishReleaseRetryLimit && isTransientPublishError(err) {
			retries++
			time.Sleep(publishReleaseRetryInterval)
			continue
		}
		return fmt.Errorf("failed to publish managed daemon release from %s to %s: %w", stage, release, err)
	}
}

func isTransientPublishError(err error) bool {
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	var errno windows.Errno
	if errors.As(err, &errno) {
		return errno == windows.ERROR_SHARING_VIOLATION || errno == windows.ERROR_ACCESS_DENIED
	}
	return false
}
