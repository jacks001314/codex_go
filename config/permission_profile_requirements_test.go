package config

import (
	"strings"
	"testing"
)

// TestPermissionProfileRequirementsValidationLikeRust pins Rust's
// validate_required_permission_profile_catalog: a managed default_permissions
// requires an allow-list, the required default must exist (explicitly or as the
// implicit workspace profile), and it must be allowed.
func TestPermissionProfileRequirementsValidationLikeRust(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "default without allow-list",
			toml: "default_permissions = \"managed\"\n",
			want: "default_permissions requires allowed_permission_profiles",
		},
		{
			name: "allow-list without a default",
			toml: "[allowed_permission_profiles]\nmanaged = true\n",
			want: "must be set unless allowed_permission_profiles allows both",
		},
		{
			name: "default outside the allow-list",
			toml: "default_permissions = \"managed\"\n[allowed_permission_profiles]\nother = true\n",
			want: "must be allowed by allowed_permission_profiles",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseRequirementsTOML([]byte(testCase.toml))
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("ParseRequirementsTOML() error = %v, want %q", err, testCase.want)
			}
		})
	}

	// The standard built-in pair implies the workspace default.
	requirements, err := ParseRequirementsTOML([]byte("[allowed_permission_profiles]\n\":workspace\" = true\n\":read-only\" = true\n"))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	selection, err := (&Config{Values: map[string]any{}, Requirements: requirements}).ResolvePermissionProfileSelection()
	if err != nil {
		t.Fatalf("ResolvePermissionProfileSelection() error = %v", err)
	}
	if selection.ProfileID != ":workspace" {
		t.Fatalf("implicit managed default = %q, want :workspace", selection.ProfileID)
	}
}

// TestPermissionProfileRequirementFallbackLikeRust mirrors Rust's
// system_allowed_permission_profiles_fall_back_from_disallowed_danger_full_access:
// a configured default the allow-list disallows falls back to the required
// default and reports a startup warning.
func TestPermissionProfileRequirementFallbackLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
default_permissions = "managed"

[allowed_permission_profiles]
managed = true

[permissions.managed]
extends = ":workspace"
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	values := map[string]any{"default_permissions": ":danger-full-access"}
	cfg := &Config{Values: values, Requirements: requirements}
	selection, err := cfg.ResolvePermissionProfileSelection()
	if err != nil {
		t.Fatalf("ResolvePermissionProfileSelection() error = %v", err)
	}
	if selection.ProfileID != "managed" {
		t.Fatalf("selection = %q, want the required default `managed`", selection.ProfileID)
	}
	resolved, err := cfg.ResolveSandboxPermissionProfile(":danger-full-access", "")
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.ID != "managed" {
		t.Fatalf("resolved = %#v, want the required default", resolved)
	}
	warnings := StartupWarnings(values, requirements)
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning, "permission_profile") && strings.Contains(warning, "disallowed by requirements") {
			found = true
		}
	}
	if !found {
		t.Fatalf("StartupWarnings() = %#v, want the permission_profile fallback notice", warnings)
	}
}
