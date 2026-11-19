package doctor

import (
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox/windowssandbox"
)

// Rust #62192: a timed out or closed registered Core exchange is reported with
// service-specific advice instead of the generic rerun guidance.
func TestWindowsSandboxDoctorAdvisesRunningServiceForRegisteredExchangeFailuresLikeRust(t *testing.T) {
	for _, code := range []windowssandbox.SetupErrorCode{
		windowssandbox.SetupErrorRegisteredServiceTimedOut,
		windowssandbox.SetupErrorRegisteredServicePipeClosed,
	} {
		home := t.TempDir()
		if err := windowssandbox.WriteSetupErrorReport(home, &windowssandbox.SetupErrorReport{
			Code:    code,
			Message: "app runtime provisioning service is unavailable; refusing helper fallback",
		}); err != nil {
			t.Fatalf("WriteSetupErrorReport() error = %v", err)
		}
		cfg := &config.Config{Values: map[string]any{"windows_sandbox": "elevated"}}
		check := applyWindowsSandboxDoctorDiagnostics(
			NewCheck("sandbox.helpers", "sandbox", CheckStatusOK, "sandbox configuration is readable"),
			cfg,
			home,
			t.TempDir(),
		)
		if check.Remediation == nil || !strings.Contains(*check.Remediation, "sandbox service is running") {
			t.Fatalf("%s remediation = %v, want service guidance", code, check.Remediation)
		}
	}
}
