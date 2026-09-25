//go:build windows

package appserverdaemon

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// ensureDetachedLaunch mirrors Rust's `ensure_detached_launch` (#42405,
// #48157): a suspended breakaway probe verifies that the caller's Job Object
// permits a detached daemon launch before an existing daemon is stopped. Job
// membership alone does not establish whether the daemon would be terminated, so
// the probe only checks that the launch itself succeeds - an outer system job may
// remain attached - and then terminates and reaps the suspended child.
func ensureDetachedLaunch(executable string) error {
	command := exec.Command(executable)
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_SUSPENDED | windows.DETACHED_PROCESS | windows.CREATE_BREAKAWAY_FROM_JOB,
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("cannot launch detached daemon; existing daemon was not stopped: %w", err)
	}
	if err := command.Process.Kill(); err != nil {
		_ = command.Wait()
		return fmt.Errorf("failed to terminate suspended launch probe: %w", err)
	}
	// Rust's `child.wait()` reports only OS failures; the probe's own exit status
	// is irrelevant because it is killed while suspended.
	if err := command.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return fmt.Errorf("failed to reap suspended launch probe: %w", err)
		}
	}
	return nil
}
