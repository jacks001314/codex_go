//go:build !windows

package sandbox

import "codex_go/codexuds"

func sharedDaemonSocketDirectory() (string, bool) {
	directory, err := codexuds.SharedDaemonSocketDirectory()
	if err != nil || directory == "" {
		return "", false
	}
	return directory, true
}
