package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
)

// TestDoctorReportsFilesystemRequirementSourceAndScopeLikeRust covers Rust
// #42821: the sandbox check names the managed filesystem source without
// exposing denied paths, and the config check states the inspected scope,
// active-thread limitation, and configuration load time.
func TestDoctorReportsFilesystemRequirementSourceAndScopeLikeRust(t *testing.T) {
	home := t.TempDir()
	denied := fmt.Sprintf("%q", filepath.Join(home, "private-doctor-control"))
	requirements := "[permissions.filesystem]\ndeny_read = [" + denied + "]\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	check := applyWindowsSandboxDoctorDiagnostics(nil, cfg, home, home)
	if !containsDetail(check, "managed filesystem source: system") {
		t.Fatalf("managed filesystem source detail missing: %#v", check.Details)
	}
	for _, detail := range check.Details {
		if filepath.Base(detail) == "private-doctor-control" || filepath.Base(detail) == "cloud-private" {
			t.Fatalf("doctor leaked a denied path: %#v", check.Details)
		}
	}

	configResult := configCheck(home, &Options{})
	for _, want := range []string{
		"configuration scope: invocation config, including cloud-managed policy",
		"active thread overrides: not inspected",
	} {
		if !containsDetail(configResult, want) {
			t.Fatalf("config check missing %q: %#v", want, configResult.Details)
		}
	}
	foundLoadDuration := false
	for _, detail := range configResult.Details {
		if len(detail) > len("configuration load ms: ") && detail[:len("configuration load ms: ")] == "configuration load ms: " {
			foundLoadDuration = true
		}
	}
	if !foundLoadDuration {
		t.Fatalf("configuration load duration missing: %#v", configResult.Details)
	}
}
