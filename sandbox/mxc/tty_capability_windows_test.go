//go:build windows

package mxc

import (
	"strings"
	"testing"
)

// TestTTYCapabilityMatchesSpawnBehaviour freezes the invariant the exec-server
// and the tty contract rely on: the capability bit and what the TTY entry point
// actually does can never disagree. Flipping nativeTTYLaunchSupported must move
// this test with it instead of leaving an assertion that hard-codes one verdict.
//
// The call uses an empty executable on purpose, so it never spawns: with the bit
// on, the entry point must fail on the executable; with the bit off it must fail
// on the capability. Spawning here would start this test binary as the helper,
// and a missing dispatch would turn into a recursive test run. The real spawn is
// covered by TestLauncherSessionDeliversConsoleContent and by the opt-in
// TestLauncherSessionRunsSandboxedCommandThroughHelper.
func TestTTYCapabilityMatchesSpawnBehaviour(t *testing.T) {
	_, err := SpawnLauncherSession("", nil, nil, "")
	if NativeTTYLaunchSupported() {
		if err == nil {
			t.Fatal("NativeTTYLaunchSupported() = true but SpawnLauncherSession accepted an empty executable")
		}
		if strings.Contains(err.Error(), TTYUnsupportedMessage) {
			t.Fatalf("error = %v, want the missing-executable diagnostic once TTY is supported", err)
		}
		return
	}
	if err == nil {
		t.Fatal("NativeTTYLaunchSupported() = false but SpawnLauncherSession accepted a TTY launch")
	}
	if !strings.Contains(err.Error(), TTYUnsupportedMessage) {
		t.Fatalf("TTY rejection = %v, want it to carry %q", err, TTYUnsupportedMessage)
	}
}

// The capability gate must run before any other launch validation: a request
// that would also fail executable validation still reports the TTY gap, so the
// diagnostic stays stable while the transport is missing. Once the transport
// exists the same call must report the validation error instead, which keeps
// this test meaningful after the capability bit flips.
func TestTTYCapabilityGatePrecedesLaunchValidation(t *testing.T) {
	_, err := SpawnLauncherSession("", nil, nil, "")
	if err == nil {
		t.Fatal("SpawnLauncherSession(\"\") accepted an empty executable")
	}
	if NativeTTYLaunchSupported() {
		if strings.Contains(err.Error(), TTYUnsupportedMessage) {
			t.Fatalf("error = %v, want the missing-executable diagnostic once TTY is supported", err)
		}
		return
	}
	if !strings.Contains(err.Error(), TTYUnsupportedMessage) {
		t.Fatalf("error = %v, want the TTY capability message before launch validation", err)
	}
}
