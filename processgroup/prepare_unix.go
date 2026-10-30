//go:build unix

package processgroup

import (
	osexec "os/exec"
	"syscall"
)

// PrepareChildSession mirrors Rust's ProcessMode::NewSession pipe launch and
// ChildCommand::terminate_on_parent_death (upstream #47605): a piped child
// starts a new session so it does not inherit the controlling TTY, and on
// Linux it is terminated when the spawning process exits. Existing
// SysProcAttr settings are preserved.
func PrepareChildSession(cmd *osexec.Cmd) {
	if cmd == nil {
		return
	}
	attr := cmd.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	attr.Setsid = true
	setParentDeathSignal(attr)
	cmd.SysProcAttr = attr
}
