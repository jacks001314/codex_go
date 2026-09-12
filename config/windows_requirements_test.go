package config

import "testing"

// TestParseRequirementsTOMLWindowsLikeRust mirrors Rust's
// deserialize_allowed_windows_sandbox_implementations: the managed Windows
// sandbox settings live in the `[windows]` table.
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
	if requirements.WindowsSandboxPrivateDesktop == nil || *requirements.WindowsSandboxPrivateDesktop {
		t.Fatalf("windows desktop = %#v", requirements.WindowsSandboxPrivateDesktop)
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

// TestResolveWindowsSandboxPrivateDesktopLikeRust covers the config chain and
// the managed override (Rust resolve_windows_sandbox_private_desktop plus
// core/src/config/requirements.rs).
func TestResolveWindowsSandboxPrivateDesktopLikeRust(t *testing.T) {
	enabled := true
	disabled := false
	tests := []struct {
		name         string
		values       map[string]any
		requirements *ConfigRequirements
		want         bool
	}{
		{name: "default", values: nil, want: true},
		{
			name:   "windows table disables",
			values: map[string]any{"windows": map[string]any{"sandbox_private_desktop": false}},
			want:   false,
		},
		{
			name:   "windows table enables",
			values: map[string]any{"windows": map[string]any{"sandbox_private_desktop": true}},
			want:   true,
		},
		{
			name:   "legacy permissions shape",
			values: map[string]any{"permissions": map[string]any{"windows_sandbox_private_desktop": false}},
			want:   false,
		},
		{
			name: "windows table beats the legacy shape",
			values: map[string]any{
				"permissions": map[string]any{"windows_sandbox_private_desktop": false},
				"windows":     map[string]any{"sandbox_private_desktop": true},
			},
			want: true,
		},
		{
			name:         "requirement enables over a disabled config",
			values:       map[string]any{"windows": map[string]any{"sandbox_private_desktop": false}},
			requirements: &ConfigRequirements{WindowsSandboxPrivateDesktop: &enabled},
			want:         true,
		},
		{
			name:         "requirement disables over an enabled config",
			values:       map[string]any{"windows": map[string]any{"sandbox_private_desktop": true}},
			requirements: &ConfigRequirements{WindowsSandboxPrivateDesktop: &disabled},
			want:         false,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ResolveWindowsSandboxPrivateDesktop(testCase.values, testCase.requirements); got != testCase.want {
				t.Fatalf("ResolveWindowsSandboxPrivateDesktop() = %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestMergeConfigRequirementsWindowsLikeRust covers the layer merge and clone.
func TestMergeConfigRequirementsWindowsLikeRust(t *testing.T) {
	disabled := false
	merged := mergeConfigRequirements(
		&ConfigRequirements{},
		&ConfigRequirements{WindowsSandboxPrivateDesktop: &disabled},
	)
	if merged.WindowsSandboxPrivateDesktop == nil || *merged.WindowsSandboxPrivateDesktop {
		t.Fatalf("merged = %#v", merged.WindowsSandboxPrivateDesktop)
	}
	if merged.WindowsSandboxPrivateDesktop == &disabled {
		t.Fatal("merge must clone the overlay pointer")
	}
	cloned := cloneRequirements(merged)
	*cloned.WindowsSandboxPrivateDesktop = true
	if *merged.WindowsSandboxPrivateDesktop {
		t.Fatal("clone aliased the original requirement")
	}
}
