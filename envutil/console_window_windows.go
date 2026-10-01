//go:build windows

package envutil

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// SuppressConsoleWindow keeps a console child from allocating its own console
// window (Windows CREATE_NO_WINDOW, Rust #48483 and #48238). A process that has
// no console of its own otherwise makes Windows hand every console child a
// brand-new console, which the default terminal app surfaces as a popped-up
// terminal window (Windows Terminal on Windows 11).
//
// The flag is ORed into whatever the caller already selected, so a caller that
// suspended the child for Job Object containment keeps CREATE_SUSPENDED. It is
// deliberately not applied to interactive programs (the user's editor, the
// desktop app launcher) or to a child that is spawned detached: Windows ignores
// CREATE_NO_WINDOW when it is combined with DETACHED_PROCESS or
// CREATE_NEW_CONSOLE, so such a child needs no help to stay console-less.
func SuppressConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
