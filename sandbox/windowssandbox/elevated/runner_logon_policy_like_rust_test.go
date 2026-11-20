package elevated

import (
	"strings"
	"testing"
)

// Rust #62593: a policy denial keeps the native code and explains which Windows
// policy settings blocked the sandbox account, and it never refreshes the stored
// credentials because a policy decision cannot be fixed by new credentials.
func TestLogonPolicyDenialExplainsWhyTheCommandCannotStartLikeRust(t *testing.T) {
	err := &RunnerLogonError{Code: errorLogonTypeNotGranted}
	message := err.Error()
	if !strings.Contains(message, "1385. Windows security policy blocked the sandbox account from signing in.") {
		t.Fatalf("logon policy denial = %q, want the 1385 policy explanation", message)
	}
	if !strings.Contains(message, "Allow log on locally") || !strings.Contains(message, "Deny log on locally") {
		t.Fatalf("logon policy denial = %q, want both logon policy settings named", message)
	}
	if !strings.Contains(message, "Contact your administrator if this PC is managed.") {
		t.Fatalf("logon policy denial = %q, want the managed-PC advice", message)
	}
	if IsRefreshableSandboxCredsError(err, nil) {
		t.Fatal("a logon policy denial must not refresh sandbox credentials")
	}
}

// Other runner logon codes keep the plain native message.
func TestOtherRunnerLogonErrorsStayUnexplainedLikeRust(t *testing.T) {
	err := &RunnerLogonError{Code: errorServiceAlreadyRunning}
	if got, want := err.Error(), "CreateProcessWithLogonW failed: 1056"; got != want {
		t.Fatalf("logon error = %q, want %q", got, want)
	}
}
