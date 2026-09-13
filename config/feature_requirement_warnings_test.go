package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManagedFeatureRequirementWarningsLikeRust mirrors Rust's
// feature_requirements_warn_on_collab_legacy_alias and
// feature_requirements_warn_and_ignore_unknown_feature: a legacy alias warns in
// favor of the canonical key, an unknown key warns that it is ignored, and
// canonical keys (including the `auto_review` alias of `guardian_approval`) stay
// quiet.
func TestManagedFeatureRequirementWarningsLikeRust(t *testing.T) {
	requirements := &ConfigRequirements{FeatureRequirements: map[string]bool{
		"collab":            true,
		"made_up_feature":   true,
		"auto_review":       false,
		"web_search_cached": true,
	}}
	warnings := StartupWarnings(map[string]any{}, requirements)
	want := []string{
		"Using legacy `features` requirement `collab` from managed requirements; prefer canonical feature key `multi_agent`",
		"Ignoring unknown `features` requirement `made_up_feature` from managed requirements",
	}
	if len(warnings) != len(want) {
		t.Fatalf("StartupWarnings() = %#v, want %#v", warnings, want)
	}
	for index, warning := range warnings {
		if warning != want[index] {
			t.Fatalf("warning[%d] = %q, want %q", index, warning, want[index])
		}
	}

	// Canonical keys and the auto_review alias produce no feature warnings.
	quiet := StartupWarnings(map[string]any{}, &ConfigRequirements{FeatureRequirements: map[string]bool{
		"multi_agent": true,
		"auto_review": true,
	}})
	if len(quiet) != 0 {
		t.Fatalf("StartupWarnings() = %#v, want none", quiet)
	}
}

// TestFeatureRequirementWarningsThroughRequirementsFile pins the load path: the
// managed feature table in requirements.toml reaches the warning channel.
func TestFeatureRequirementWarningsThroughRequirementsFile(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("[features]\ncollab = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(home)
	if _, err := service.Read(&ConfigReadParams{}); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	found := false
	for _, warning := range service.Warnings() {
		if strings.Contains(warning.Summary, "Using legacy `features` requirement `collab`") {
			found = true
		}
	}
	if !found {
		t.Fatalf("config warnings = %#v, want the collab alias notice", service.Warnings())
	}
}
