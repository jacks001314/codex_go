//go:build unix && !linux

package processgroup

import "syscall"

// setParentDeathSignal is a no-op outside Linux, matching Rust's non-Linux
// stub.
func setParentDeathSignal(*syscall.SysProcAttr) {}
