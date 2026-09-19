
package config

import (
	"strings"
	"testing"
)

// Mirrors Rust #46554: the managed Windows sandbox settings live in the
// `[windows]` table, and the removed sandbox_private_desktop setting is ignored
// (legacy Windows sandboxes always use a private desktop).
func TestParseRequirementsTOMLWindowsLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[windows]
allowed_sandbox_implementations = ["elevated"]
sandbox_private_desktop = false
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil {
		t.Fatal("requirements = nil")
	}
	if len(requirements.AllowedWindowsSandboxImplementations) != 1 ||
		requirements.AllowedWindowsSandboxImplementations[0] != WindowsSandboxSetupElevated {
		t.Fatalf("allowed implementations = %#v", requirements.AllowedWindowsSandboxImplementations)
	}
}

// TestParseRequirementsTOMLWindowsEmptyImplementationsLikeRust mirrors Rust's
// empty_allowed_windows_sandbox_implementations_is_rejected.
func TestParseRequirementsTOMLWindowsEmptyImplementationsLikeRust(t *testing.T) {
	if _, err := ParseRequirementsTOML([]byte(`
[windows]
allowed_sandbox_implementations = []
`)); err == nil {
		t.Fatal("an empty allowed_sandbox_implementations must be rejected")
	}
}

// TestParseRequirementsTOMLWindowsLegacyTopLevelLikeRust keeps the older Go
// top-level key working while the nested table stays canonical.
func TestParseRequirementsTOMLWindowsLegacyTopLevelLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`allowed_windows_sandbox_implementations = ["unelevated"]`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || len(requirements.AllowedWindowsSandboxImplementations) != 1 ||
		requirements.AllowedWindowsSandboxImplementations[0] != WindowsSandboxSetupUnelevated {
		t.Fatalf("legacy requirements = %#v", requirements)
	}

	nestedWins, err := ParseRequirementsTOML([]byte(`
allowed_windows_sandbox_implementations = ["unelevated"]

[windows]
allowed_sandbox_implementations = ["elevated"]
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if len(nestedWins.AllowedWindowsSandboxImplementations) != 1 ||
		nestedWins.AllowedWindowsSandboxImplementations[0] != WindowsSandboxSetupElevated {
		t.Fatalf("nested [windows] must win: %#v", nestedWins.AllowedWindowsSandboxImplementations)
	}
}

// TestWindowsSandboxPrivateDesktopSettingIsObsoleteLikeRust mirrors Rust #46554:
// the removed `[windows] sandbox_private_desktop` setting no longer reaches the
// requirements, and both the canonical and the legacy spelling are reported as
// ignored keys with the migration hint.
func TestWindowsSandboxPrivateDesktopSettingIsObsoleteLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[windows]
sandbox_private_desktop = false
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements != nil && requirements.AllowedWindowsSandboxImplementations != nil {
		t.Fatalf("requirements = %#v, want only the obsolete key ignored", requirements)
	}

	for name, values := range map[string]map[string]any{
		"windows table": {"windows": map[string]any{"sandbox_private_desktop": false}},
		"legacy shape":  {"permissions": map[string]any{"windows_sandbox_private_desktop": false}},
	} {
		t.Run(name, func(t *testing.T) {
			layers := []Layer{{
				Name:   LayerSource{Type: LayerSourceUser, File: "config.toml"},
				Config: values,
			}}
			warning := IgnoredConfigWarning(layers, nil)
			want := " Remove windows.sandbox_private_desktop; legacy Windows sandboxes always use a private desktop."
			if !strings.Contains(warning, "is ignored."+want) {
				t.Fatalf("warning = %q, want the migration hint %q", warning, want)
			}
		})
	}
}

