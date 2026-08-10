//go:build !windows

package appserver

import (
	osexec "os/exec"
	"syscall"
)

// hookProcessTree runs a hook command in its own process group so the whole
// tree can be terminated on timeout or cancellation (Rust dd916428cd).
type hookProcessTree struct {
	cmd *osexec.Cmd
}

func startHookProcessTree(cmd *osexec.Cmd) (*hookProcessTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
