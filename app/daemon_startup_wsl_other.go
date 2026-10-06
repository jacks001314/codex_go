//go:build !linux

package app

// usesWSLDrvfs only applies on Linux hosts; other platforms never run the WSL
// filesystem probe (Rust daemon_startup::uses_wsl_drvfs' non-Linux branch).
func usesWSLDrvfs(codexHome string) bool {
	return false
}
