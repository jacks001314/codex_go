//go:build unix

package processgroup

import (
	"os"
	"syscall"
)

// TerminateProcessTree kills the process group led by process, mirroring the
// Rust pipe adapter's PipeChildTerminator::kill. A process that does not lead
// its own group falls back to the direct child so an unrelated group is never
// signalled.
func TerminateProcessTree(process *os.Process) error {
	return signalOwnedGroup(process, syscall.SIGKILL, Kill)
}

// InterruptProcessTree sends SIGINT to the process group led by process,
// mirroring PipeChildTerminator::signal. See TerminateProcessTree for the
// non-leader fallback.
func InterruptProcessTree(process *os.Process) error {
	return signalOwnedGroup(process, syscall.SIGINT, Interrupt)
}

func signalOwnedGroup(process *os.Process, sig syscall.Signal, group func(int) error) error {
	if process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(process.Pid)
	if err == nil && pgid == process.Pid {
		if err := group(process.Pid); err == nil {
			return nil
		}
	}
	return process.Signal(sig)
}
