package sandbox

import "testing"

// TestDetermineWindowsReadinessTreatsMxcAsReadyLikeRust mirrors Rust #46271: a
// selected MXC backend reports the Windows sandbox as ready, because it skips
// the legacy setup and readiness APIs.
func TestDetermineWindowsReadinessTreatsMxcAsReadyLikeRust(t *testing.T) {
	for _, setupComplete := range []bool{false, true} {
		response := DetermineWindowsReadinessFromState(WindowsSandboxMxc, setupComplete)
		if response == nil || response.Status != WindowsReadinessReady {
			t.Fatalf("mxc readiness (setupComplete=%v) = %#v, want ready", setupComplete, response)
		}
	}
	// The legacy levels keep their existing behaviour.
	if got := DetermineWindowsReadinessFromState(WindowsSandboxElevated, false); got.Status != WindowsReadinessUpdateRequired {
		t.Fatalf("elevated readiness without setup = %#v, want updateRequired", got)
	}
	if got := DetermineWindowsReadinessFromState(WindowsSandboxUnelevated, false); got.Status != WindowsReadinessReady {
		t.Fatalf("unelevated readiness = %#v, want ready", got)
	}
	if got := DetermineWindowsReadinessFromState(WindowsSandboxDisabled, false); got.Status != WindowsReadinessNotConfigured {
		t.Fatalf("disabled readiness = %#v, want notConfigured", got)
	}
}
