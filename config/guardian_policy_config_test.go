package config

import "testing"

// TestGuardianPolicyConfigLikeRust mirrors Rust Config::guardian_policy_config:
// the managed `guardian_policy_config` requirement wins over `[auto_review]
// policy`, a value that normalizes to empty is treated as unset so the
// lower-priority layer still applies, and `[auto_review]
// experimental_policy_template` is the reviewer prompt template.
func TestGuardianPolicyConfigLikeRust(t *testing.T) {
	managed := "  Managed policy.  "
	cfg := &Config{
		Values: map[string]any{"auto_review": map[string]any{
			"policy":                       "Config policy.",
			"experimental_policy_template": "Template: {{ tenant_policy_config }}",
		}},
		Requirements: &ConfigRequirements{GuardianPolicyConfig: &managed},
	}
	if got, ok := cfg.GuardianPolicyConfig(); !ok || got != "Managed policy." {
		t.Fatalf("GuardianPolicyConfig() = %q, %v, want the managed policy", got, ok)
	}
	if got, ok := cfg.GuardianPolicyTemplate(); !ok || got != "Template: {{ tenant_policy_config }}" {
		t.Fatalf("GuardianPolicyTemplate() = %q, %v", got, ok)
	}

	// A requirement that normalizes to empty falls back to the config value.
	empty := " \n\t "
	cfg.Requirements.GuardianPolicyConfig = &empty
	if got, ok := cfg.GuardianPolicyConfig(); !ok || got != "Config policy." {
		t.Fatalf("GuardianPolicyConfig() with an empty requirement = %q, %v", got, ok)
	}

	// Unset everywhere reports no override.
	if got, ok := (&Config{}).GuardianPolicyConfig(); ok || got != "" {
		t.Fatalf("GuardianPolicyConfig() = %q, %v, want unset", got, ok)
	}
	if got, ok := (&Config{}).GuardianPolicyTemplate(); ok || got != "" {
		t.Fatalf("GuardianPolicyTemplate() = %q, %v, want unset", got, ok)
	}
}

// TestRequirementsFileParsesGuardianPolicyConfig mirrors Rust
// requirements_toml.guardian_policy_config.
func TestRequirementsFileParsesGuardianPolicyConfig(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte("guardian_policy_config = \"  Managed policy.  \"\n"))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	// Go's requirements parser trims string requirements (as it does for the
	// other managed paths); the Config accessor normalizes again, so an
	// empty-after-trim requirement still lets the config value apply.
	if requirements == nil || requirements.GuardianPolicyConfig == nil || *requirements.GuardianPolicyConfig != "Managed policy." {
		t.Fatalf("requirements = %#v, want the managed policy", requirements)
	}
}
