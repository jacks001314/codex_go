//go:build windows

package windowssandbox

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestPreparedSandboxCleanupRefusesAMissingGuard(t *testing.T) {
	var prepared *PreparedSandboxCleanup
	if err := prepared.Finish(nil, nil, nil); err == nil || err.Error() != "missing prepared sandbox cleanup" {
		t.Fatalf("Finish(nil guard) error = %v", err)
	}
	prepared.Release()

	empty := &PreparedSandboxCleanup{}
	if err := empty.Finish(nil, nil, nil); err == nil || err.Error() != "missing prepared sandbox cleanup" {
		t.Fatalf("Finish(released guard) error = %v", err)
	}
}

func TestCaptureRetainedLogonsRefusesTooManyTokensLikeRust(t *testing.T) {
	_, err := captureRetainedLogons([]windows.Token{1, 2, 3}, &disabledSandboxUsers{})
	if err == nil || err.Error() != "too many retained cleanup tokens" {
		t.Fatalf("captureRetainedLogons(3 tokens) error = %v", err)
	}
}

func TestRetainedLogonsOnlyPinTheirOwnSessionsLikeRust(t *testing.T) {
	retained := &retainedLogons{ids: []windows.LUID{{LowPart: 0x1234}}}
	if !retained.contains(windows.LUID{LowPart: 0x1234}) {
		t.Fatal("retained logon was not recognized")
	}
	if retained.contains(windows.LUID{LowPart: 0x1235}) || retained.contains(windows.LUID{LowPart: 0x1234, HighPart: 1}) {
		t.Fatal("retained logons matched a foreign session")
	}
	var missing *retainedLogons
	if missing.contains(windows.LUID{LowPart: 0x1234}) {
		t.Fatal("a missing guard pinned a session")
	}
}
