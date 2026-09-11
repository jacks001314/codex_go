//go:build unix

package appserverdaemon

import "syscall"

// reapZombiePIDProcess reaps an unreaped child (best effort, non-blocking) so a
// zombie app-server or updater does not linger after being observed inactive
// (Rust #43504). Re-exec can lose the Go child handle without changing
// parenthood, so waitpid is attempted even when the handle is gone; a non-child
// returns ECHILD and is ignored.
func reapZombiePIDProcess(pid uint32) {
	if pid == 0 {
		return
	}
	var status syscall.WaitStatus
	_, _ = syscall.Wait4(int(pid), &status, syscall.WNOHANG, nil)
}
