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

import osexec "os/exec"

// snapshotCaptureGroupKiller takes a failed capture's process group down. The
// production value is the platform's group cleanup; it stays a variable so the
// cleanup the runner performs is observable on a host without process groups.
var snapshotCaptureGroupKiller = killSnapshotCaptureProcessGroup

// runSnapshotCaptureProcess runs one capture command with its process group
// owned for the length of the run and reports a failure to that group's
// cleanup, so no startup child outlives the capture.
func runSnapshotCaptureProcess(process *osexec.Cmd) error {
	ownSnapshotCaptureProcessGroup(process)
	err := process.Run()
	if err != nil {
		// Only a failed capture cleans up: a successful capture keeps the
		// helpers its startup files left behind.
		_ = snapshotCaptureGroupKiller(process)
	}
	return err
}
