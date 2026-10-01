//go:build !windows

package envutil

import "os/exec"

// SuppressConsoleWindow is a no-op outside Windows: only Windows allocates a
// console window for a non-interactive child process.
func SuppressConsoleWindow(*exec.Cmd) {}
