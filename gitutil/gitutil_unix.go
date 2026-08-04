//go:build !windows

package gitutil

import (
	"os/exec"
	"syscall"
)

type gitProcessTree struct {
	cmd *exec.Cmd
}

func startGitTree(cmd *exec.Cmd, respawn func() *exec.Cmd) (*gitProcessTree, error) {
	_ = respawn
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &gitProcessTree{cmd: cmd}, nil
}

func (t *gitProcessTree) wait() error {
	if t == nil {
		return nil
	}
	return t.cmd.Wait()
}

func (t *gitProcessTree) kill() {
	if t == nil || t.cmd == nil || t.cmd.Process == nil {
		return
	}
	// Negative PID signals the whole process group (Rust kill_process_group).
	_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
}
