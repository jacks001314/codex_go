//go:build unix

package tool

import (
	"errors"
	"os"
	osexec "os/exec"
	"syscall"

	"codex_go/processgroup"
)

// ownSnapshotCaptureProcessGroup gives the capture its own process group
// (Rust's Command::process_group(0)) so the cleanup SnapshotCapture arms can
// reach the startup files' children while the leader stays unreaped. Os/exec's
// own cancellation kills only the capture process, which is how those children
// survived a failed capture. Cancellation is wired in runSnapshotCaptureProcess
// rather than through Cmd.Cancel, because os/exec rejects a Cancel on a command
// that was not built by CommandContext.
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
	err := processgroup.Kill(pid)
	if err == nil {
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
