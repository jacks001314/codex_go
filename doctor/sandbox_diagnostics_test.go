package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/sandbox/windowssandbox"
)

func TestWindowsSandboxDoctorDiagnosticsReportSetupFailureLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := windowssandbox.WriteSetupErrorReport(home, &windowssandbox.SetupErrorReport{
		Code:    windowssandbox.SetupErrorOrchestratorElevationRequired,
		Message: "elevation required for sandbox setup",
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
	if check == nil || check.Status != CheckStatusWarning || check.Summary != "Windows sandbox setup failed" {
		t.Fatalf("check = %#v", check)
	}
	foundLevel := false
	foundError := false
	for _, detail := range check.Details {
		if detail == "windows sandbox level: elevated" {
			foundLevel = true
		}
		if detail == "windows sandbox setup error: elevation required for sandbox setup" {
			foundError = true
		}
	}
	if !foundLevel || !foundError {
		t.Fatalf("details = %#v", check.Details)
	}
	if check.Remediation == nil || *check.Remediation == "" {
		t.Fatalf("remediation missing: %#v", check)
	}
	_ = os.Remove(filepath.Join(home, ".sandbox", "setup-error.json"))
}

func TestWindowsSandboxDoctorDiagnosticsReportDeniedReadRestrictions(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"windows_sandbox": "default"}}
	check := applyWindowsSandboxDoctorDiagnostics(
		NewCheck("sandbox.helpers", "sandbox", CheckStatusOK, "sandbox configuration is readable"),
		cfg,
		t.TempDir(),
		t.TempDir(),
	)
	if check == nil {
		t.Fatal("check is nil")
	}
	if !containsDetail(check, "denied-read restrictions: inactive") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestWindowsSandboxDoctorDiagnosticsReportGlobDepthAndManagedSource(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"windows_sandbox": "default"}}
	check := applyWindowsSandboxDoctorDiagnostics(
		NewCheck("sandbox.helpers", "sandbox", CheckStatusOK, "sandbox configuration is readable"),
		cfg,
		t.TempDir(),
		t.TempDir(),
	)
	if !containsDetail(check, "glob scan max depth: unbounded") {
		t.Fatalf("glob depth detail missing: %#v", check.Details)
	}
	if !containsDetail(check, "managed filesystem source: none") {
		t.Fatalf("managed filesystem source detail missing: %#v", check.Details)
	}
}
