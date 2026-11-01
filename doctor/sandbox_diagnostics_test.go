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
		if detail == "sandbox backend: elevated" {
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
	if !containsDetail(check, "denied-read restrictions: false") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestWindowsSandboxBackendRowMatchesRust(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
		want   string
	}{
		{"unconfigured", map[string]any{}, "disabled"},
		{"nested disabled", map[string]any{"windows": map[string]any{"sandbox": "disabled"}}, "disabled"},
		{"nested elevated", map[string]any{"windows": map[string]any{"sandbox": "elevated"}}, "elevated"},
		{"legacy unelevated", map[string]any{"windows_sandbox": "unelevated"}, "restricted-token"},
		{"feature elevated", map[string]any{"features": map[string]any{"elevated_windows_sandbox": true}}, "elevated"},
		{"mxc", map[string]any{"windows": map[string]any{"sandbox": "mxc"}}, "mxc"},
	}
	for _, tc := range cases {
		cfg := &config.Config{Values: tc.values}
		check := applyWindowsSandboxDoctorDiagnostics(
			NewCheck("sandbox.helpers", "sandbox", CheckStatusOK, "sandbox configuration is readable"),
			cfg,
			t.TempDir(),
			t.TempDir(),
		)
		if !containsDetail(check, "sandbox backend: "+tc.want) {
			t.Fatalf("%s: details = %#v", tc.name, check.Details)
		}
	}
}

func TestSandboxCheckAppendsWindowsRowsAfterHelperLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"sandbox_mode": "read-only"}}
	check := applyWindowsSandboxDoctorDiagnostics(
		sandboxCheckFromConfig(cfg, nil, ""),
		cfg,
		t.TempDir(),
		t.TempDir(),
	)
	last := check.Details[len(check.Details)-2:]
	if last[0] != "sandbox backend: disabled" || last[1] != "denied-read restrictions: false" {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestWindowsSandboxDoctorDiagnosticsReportGlobDepthAndManagedSource(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"windows_sandbox": "default"}}
	check := sandboxCheckFromConfig(cfg, nil, "")
	if !containsDetail(check, "glob scan max depth: unbounded") {
		t.Fatalf("glob depth detail missing: %#v", check.Details)
	}
	if !containsDetail(check, "managed filesystem source: none") {
		t.Fatalf("managed filesystem source detail missing: %#v", check.Details)
	}
}
