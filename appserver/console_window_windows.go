//go:build windows

package appserver

import (
	osexec "os/exec"

	"codex_go/envutil"
)

// suppressChildConsoleWindow mirrors Rust #48483: the pty child-command wrapper
// sets CREATE_NO_WINDOW, so a piped child launched from a detached Windows
// process never allocates a console window. The flag is ORed in so a caller
// that already selected CREATE_SUSPENDED (Job Object containment) keeps it:
// Go sets creation flags directly, where Rust's `creation_flags` replaces them.
func suppressChildConsoleWindow(cmd *osexec.Cmd) {
	envutil.SuppressConsoleWindow(cmd)
}
