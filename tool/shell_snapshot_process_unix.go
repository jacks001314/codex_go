//go:build unix

package tool

import (
	"errors"
	"os"
	osexec "os/exec"
	"syscall"
)

// ownSnapshotCaptureProcessGroup gives the capture its own process group and
// routes cancellation to the group kill (Rust's Command::process_group(0) plus
// the cleanup SnapshotCapture arms while the leader stays unreaped). Os/exec's
// own cancellation kills only the capture process, which is how the startup
// files' children survived a failed capture.
func ownSnapshotCaptureProcessGroup(process *osexec.Cmd) {
	if process == nil {
		return
	}
	attr := process.SysProcAttr
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	attr.Setpgid = true
	process.SysProcAttr = attr
	process.Cancel = func() error {
		return killSnapshotCaptureProcessGroup(process)
	}
}

// killSnapshotCaptureProcessGroup signals every process the capture started.
// The capture leads its group, so its PID is the group id; a group without
// members left reports ESRCH and needs no cleanup.
func killSnapshotCaptureProcessGroup(process *osexec.Cmd) error {
	if process == nil || process.Process == nil {
		return nil
	}
	pid := process.Process.Pid
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	// The capture did not lead a group: fall back to the leader, and report an
	// already finished process as finished so a cancellation keeps its error.
	killErr := process.Process.Kill()
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return err
	}
	return nil
}
