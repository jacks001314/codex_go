//go:build !unix

package processgroup

import "os"

// TerminateProcessTree kills only the direct child outside Unix, where Rust's
// process-group helpers are no-ops.
func TerminateProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}

// InterruptProcessTree signals only the direct child outside Unix, where
// Rust's process-group helpers are no-ops.
func InterruptProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Signal(os.Interrupt)
}
