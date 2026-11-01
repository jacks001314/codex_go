package tool

// Owning a capture's process group until its output and exit status are
// validated.
//
// Rust parity: codex-rs/exec-server/src/shell_snapshot_process.rs (#49782). A
// failed shell snapshot capture used to leave the processes the user's shell
// startup files had launched running after the capture ended, so each capture
// now leads its own process group and that group is killed when the capture
// fails, times out, or is cancelled. A successful capture keeps its startup
// helpers, because the state it captured may still depend on them (Rust's
// SnapshotCapture::preserve_helpers releases the group guard).

import (
	"context"
	osexec "os/exec"
	"sync"
)

// snapshotCaptureGroupKiller takes a failed capture's process group down. The
// production value is the platform's group cleanup; it stays a variable so the
// cleanup the runner performs is observable on a host without process groups.
var snapshotCaptureGroupKiller = killSnapshotCaptureProcessGroup

// runSnapshotCaptureProcess runs one capture command with its process group
// owned for the length of the run and reports a failure to that group's
// cleanup, so no startup child outlives the capture.
//
// Cancellation reaches the group here rather than through Cmd.Cancel: os/exec
// only honours Cmd.Cancel on commands built by CommandContext, and assigning it
// to an ordinary Command makes every run fail with "command with a non-nil
// Cancel was not created with CommandContext". The kill runs while the leader
// is still unreaped, which is what Rust's guard relies on to reach the startup
// files' children.
func runSnapshotCaptureProcess(ctx context.Context, process *osexec.Cmd) error {
	ownSnapshotCaptureProcessGroup(process)
	var once sync.Once
	killGroup := func() {
		once.Do(func() { _ = snapshotCaptureGroupKiller(process) })
	}
	done := make(chan struct{})
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				killGroup()
			case <-done:
			}
		}()
	}
	err := process.Run()
	close(done)
	if err != nil {
		// Only a failed capture cleans up: a successful capture keeps the
		// helpers its startup files left behind.
		killGroup()
	}
	return err
}
