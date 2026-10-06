//go:build linux

package app

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// v9fsSuperMagic is the 9p filesystem magic a Windows-mounted WSL DrvFS home
// reports (Rust #50555).
const v9fsSuperMagic = 0x01021997

// usesWSLDrvfs reports whether codexHome resolves onto a Windows-mounted WSL
// filesystem. CODEX_HOME may not exist yet, so an existing ancestor is probed
// (Rust daemon_startup::uses_wsl_drvfs).
func usesWSLDrvfs(codexHome string) bool {
	if !isWSLHost() {
		return false
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return false
	}
	path := codexHome
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false
		}
		path = parent
	}
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil {
		return false
	}
	return uint64(stats.Type) == v9fsSuperMagic
}

// isWSLHost detects WSL the way Rust's `is_wsl` does: the interop environment
// variable, or a Microsoft-tagged kernel version.
func isWSLHost() bool {
	if strings.TrimSpace(os.Getenv("WSL_INTEROP")) != "" {
		return true
	}
	version, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(version))
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
}
