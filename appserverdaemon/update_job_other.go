//go:build !windows

package appserverdaemon

import (
	"context"
	"fmt"
)

// runWindowsUpdateInstaller is only reachable on Windows; InstallLatestStandalone
// guards the call with runtime.GOOS. The stub keeps the package buildable on
// every other platform (see update_job_windows.go).
func runWindowsUpdateInstaller(context.Context, string, []string) error {
	return fmt.Errorf("windows standalone updater is unavailable on this platform")
}
