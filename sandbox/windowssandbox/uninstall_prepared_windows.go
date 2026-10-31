//go:build windows

package windowssandbox

import (
	"errors"

	"golang.org/x/sys/windows"
)

// RetainedLogonToken is a Windows logon token that must survive sandbox
// cleanup (Rust retains the trusted runtime finalizer's private logins).
type RetainedLogonToken = windows.Token

// PreparedSandboxCleanup holds the machine-wide setup lock after the sandbox
// accounts were disabled and their processes and logon tokens exited, while
// every network protection and filesystem permission is still intact (Rust
// PreparedWindowsSandboxCleanup, #42375).
//
// Keep the guard on the goroutine that prepared it. Finish removes the
// remaining resources; releasing it only drops the lock and never re-enables
// the accounts or restores anything that was already removed.
type PreparedSandboxCleanup struct {
	lock  *SandboxSetupLock
	users *disabledSandboxUsers
}

// PrepareSandboxCleanup serializes with setup, disables the sandbox accounts
// and waits for their processes and logon tokens to exit before any protection
// is removed (Rust prepare_packaged_windows_sandbox_cleanup). A preparation
// failure restores the original account flags before the setup lock is
// released.
func PrepareSandboxCleanup() (*PreparedSandboxCleanup, error) {
	return prepareSandboxCleanupGuard(nil)
}

// PrepareSandboxCleanupWithRetainedTokens retains only the trusted runtime
// finalizer's private logins while stopping sandbox work. The tokens must be
// fresh, private logins for distinct managed sandbox accounts; ordinary
// processes are still stopped (Rust
// prepare_packaged_windows_sandbox_cleanup_with_retained_tokens).
func PrepareSandboxCleanupWithRetainedTokens(tokens []RetainedLogonToken) (*PreparedSandboxCleanup, error) {
	return prepareSandboxCleanupGuard(tokens)
}

// prepareSandboxCleanupGuard serializes with setup and disables the accounts
// before any protection is removed.
func prepareSandboxCleanupGuard(retainedTokens []windows.Token) (*PreparedSandboxCleanup, error) {
	lock, err := AcquireSandboxSetupLock(5000)
	if err != nil {
		return nil, err
	}
	users, err := prepareSandboxCleanupForRemovalWithRetained(retainedTokens)
	if err != nil {
		lock.Release()
		return nil, err
	}
	return &PreparedSandboxCleanup{lock: lock, users: users}, nil
}

// Finish removes resources while retaining the setup lock and disabled
// accounts. A non-nil codexHome keeps its ancestors pinned until
// cleanUpDesktop starts; independent cleanup steps continue after an error, so
// a failed Finish may be retried with the same identities. The caller owns the
// guard and must call Release when it is done (Rust Drop).
func (prepared *PreparedSandboxCleanup) Finish(codexHome *string, report func(string), cleanUpDesktop func() error) error {
	if prepared == nil || prepared.lock == nil {
		return errors.New("missing prepared sandbox cleanup")
	}
	if prepared.users != nil {
		if err := prepared.users.validateCurrent(); err != nil {
			return err
		}
	}
	return finishSandboxCleanup(codexHome, report, cleanUpDesktop, defaultSandboxCleanupSteps())
}

// Release drops the setup lock without removing any resource (Rust Drop). The
// sandbox accounts stay disabled with their protections intact.
func (prepared *PreparedSandboxCleanup) Release() {
	if prepared == nil || prepared.lock == nil {
		return
	}
	if prepared.users != nil {
		prepared.users.retained.close()
	}
	prepared.lock.Release()
	prepared.lock = nil
}
