//go:build !windows

package appserver

import osexec "os/exec"

// suppressChildConsoleWindow is a no-op off Windows: only Windows allocates a
// console window for a piped child process (Rust #48483).
func suppressChildConsoleWindow(cmd *osexec.Cmd) {}
