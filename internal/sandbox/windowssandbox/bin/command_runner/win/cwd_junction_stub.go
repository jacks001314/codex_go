//go:build !windows

package win

import "codex_go/internal/sandbox/windowssandbox"

func createCWDJunction(cwd string, logDir string) (string, error) {
	return "", windowssandbox.Unsupported("bin.command_runner.win.cwd_junction")
}

func IsReparsePoint(path string) (bool, error) {
	return false, windowssandbox.Unsupported("bin.command_runner.win.cwd_junction.reparse")
}
