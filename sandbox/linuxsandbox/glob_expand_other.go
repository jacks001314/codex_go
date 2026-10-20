//go:build !linux

package linuxsandbox

import "errors"

func expandUnreadableGlobsInProcess([]string, string, *int) ([]string, error) {
	return nil, errors.New("Linux deny-glob expansion is unavailable on this platform")
}
