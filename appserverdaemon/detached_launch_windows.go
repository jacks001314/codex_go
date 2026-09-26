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
// #48157, #48491): a suspended breakaway probe verifies that the caller's Job
// Object permits a detached daemon launch before an existing daemon is stopped.
// Job membership alone does not establish whether the daemon would be
// terminated, so the probe only checks that the launch itself succeeds - an
// outer system job may remain attached - and then terminates and reaps the
// suspended child. A launch denied only by the breakaway flag is classified as
// `DetachedLaunchRestrictedError`, so automatic startup can fall back to the
// embedded server where an explicit lifecycle operation must still fail.
func ensureDetachedLaunch(executable string) error {
	breakaway := exec.Command(executable)
	breakaway.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_SUSPENDED | windows.DETACHED_PROCESS | windows.CREATE_BREAKAWAY_FROM_JOB,
	}
	if err := breakaway.Start(); err != nil {
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("cannot launch detached daemon: %w", err)
		}
		// Access denied can also mean the file cannot be executed. Only classify a
		// job restriction when removing breakaway makes the launch succeed. The
		// diagnostic child stays suspended and is always reaped.
		plain := exec.Command(executable)
		plain.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: windows.CREATE_SUSPENDED | windows.DETACHED_PROCESS,
		}
		if plainErr := plain.Start(); plainErr != nil {
			return fmt.Errorf("cannot launch detached daemon: %w", err)
		}
		if reapErr := reapSuspendedLaunchProbe(plain); reapErr != nil {
			return reapErr
		}
		return &DetachedLaunchRestrictedError{err: err}
	}
	return reapSuspendedLaunchProbe(breakaway)
}

// reapSuspendedLaunchProbe terminates and reaps a suspended probe child.
// Rust's `child.wait()` reports only OS failures; the probe's own exit status
// is irrelevant because it is killed while suspended.
func reapSuspendedLaunchProbe(command *exec.Cmd) error {
	if err := command.Process.Kill(); err != nil {
		_ = command.Wait()
		return fmt.Errorf("failed to terminate suspended launch probe: %w", err)
	}
	if err := command.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return fmt.Errorf("failed to reap suspended launch probe: %w", err)
		}
	}
	return nil
}
