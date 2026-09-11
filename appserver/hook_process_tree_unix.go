//go:build !windows

package appserver

import (
	osexec "os/exec"
	"syscall"
)

// hookProcessTree runs a hook command in its own session and process group so
// the whole tree can be terminated on timeout or cancellation (Rust
// dd916428cd). A new session also detaches the command from the controlling
// terminal, where shell startup code that touches the tty could otherwise stop
// the hook on background terminal I/O (Rust #43876).
type hookProcessTree struct {
	cmd *osexec.Cmd
}

func startHookProcessTree(cmd *osexec.Cmd) (*hookProcessTree, error) {
	// `setsid` makes the child a session and process-group leader, so killing
	// the negative PID still tears down the whole tree while the child no longer
	// inherits the controlling terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &hookProcessTree{cmd: cmd}, nil
}

func (t *hookProcessTree) wait() error {
	if t == nil || t.cmd == nil {
		return nil
	}
	return t.cmd.Wait()
}

// terminate kills the whole process group (negative PID, Rust kill_process_group).
func (t *hookProcessTree) terminate() {
	if t == nil || t.cmd == nil || t.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
}

// preserveDescendants is a no-op on Unix: successfully completed hooks may
// intentionally leave detached helpers running, and nothing is terminated
// unless terminate() is called explicitly.
func (t *hookProcessTree) preserveDescendants() {}
