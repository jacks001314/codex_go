//go:build linux

package processgroup

import "syscall"

// setParentDeathSignal arranges for the child to receive SIGTERM when the
// spawning parent exits, matching Rust's set_parent_death_signal.
func setParentDeathSignal(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGTERM
}
