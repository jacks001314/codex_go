//go:build !unix

package tool

import osexec "os/exec"

// ownSnapshotCaptureProcessGroup keeps the platform's default ownership. The
// upstream cleanup is Unix-only (#49782: `#[cfg(unix)]`), so a host without
// process groups keeps exec's own cancellation of the capture process.
func ownSnapshotCaptureProcessGroup(process *osexec.Cmd) {
	_ = process
}

// killSnapshotCaptureProcessGroup has no process group to signal here.
func killSnapshotCaptureProcessGroup(process *osexec.Cmd) error {
	_ = process
	return nil
}
