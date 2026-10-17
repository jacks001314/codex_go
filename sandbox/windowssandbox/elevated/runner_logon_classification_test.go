package elevated

import (
	"errors"
	"testing"
)

// TestRunnerLogonErrorClassificationLikeRust mirrors Rust #47695's
// account_logon_error: only native bad/expired passwords become a
// SandboxAccountCredentialMismatch, and the underlying Windows error stays
// available.
func TestRunnerLogonErrorClassificationLikeRust(t *testing.T) {
	for _, code := range []uint32{errorLogonFailure, errorPasswordExpired, errorPasswordMustChange} {
		err := classifyRunnerLogonError(code)
		var mismatch *SandboxAccountCredentialMismatch
		if !errors.As(err, &mismatch) {
			t.Fatalf("code %d classified as %v, want a credential mismatch", code, err)
		}
		var logonError *RunnerLogonError
		if !errors.As(err, &logonError) || logonError.Code != code {
			t.Fatalf("code %d did not preserve the runner logon error: %v", code, err)
		}
	}
	for _, code := range []uint32{5, errorNoSuchLogonSession, errorServiceAlreadyRunning, errorNotFound} {
		err := classifyRunnerLogonError(code)
		var mismatch *SandboxAccountCredentialMismatch
		if errors.As(err, &mismatch) {
			t.Fatalf("code %d must not be classified as a credential mismatch", code)
		}
		var logonError *RunnerLogonError
		if !errors.As(err, &logonError) || logonError.Code != code {
			t.Fatalf("code %d lost the runner logon error: %v", code, err)
		}
	}
}
